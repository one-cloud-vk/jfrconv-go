#!/usr/bin/env python3
"""Canonical decoder for the OTLP schema emitted by pinned async-profiler v4.5.

This validation-only decoder is independent of the native encoder. It resolves
all global dictionaries and retains profile boundaries, selected sample/period
types, periods, thread attributes, and every ordered timestamp/value pair.
Explicit values and the per-chunk implicit-one representation stay distinct.
It deliberately rejects the incompatible v4.1 dictionary/sample layout.
"""
from pathlib import Path
import hashlib
import json
import struct
import sys

from protobuf_wire import protobuf, integers


def _scalar(fields, number):
    values = integers(fields, number)
    assert len(values) <= 1, f'duplicate scalar field {number}'
    return values[0] if values else 0


def _message(fields, number):
    values = [value for n, wire, value in fields if n == number and wire == 2]
    assert len(values) == 1, f'expected exactly one message field {number}'
    return protobuf(values[0])


def _fixed64(fields, number):
    values = [value for n, wire, value in fields if n == number and wire == 1]
    assert len(values) <= 1, f'duplicate fixed64 field {number}'
    return struct.unpack('<Q', values[0])[0] if values else 0


def _table(fields, number):
    return [protobuf(value) for n, wire, value in fields if n == number and wire == 2]


def semantic_otlp(path):
    top = protobuf(Path(path).read_bytes())
    dictionary = _message(top, 2)
    strings = [value.decode('utf-8') for n, wire, value in dictionary if n == 5 and wire == 2]
    assert strings and strings[0] == '', 'string_table[0] must be empty'
    functions = [strings[_scalar(f, 1)] for f in _table(dictionary, 3)]
    assert functions and functions[0] == '', 'function_table[0] must be empty'
    locations = [tuple((functions[_scalar(line, 1)], _scalar(line, 2))
                       for line in _table(location, 3))
                 for location in _table(dictionary, 2)]
    assert locations and locations[0] == (), 'location_table[0] must be empty'
    stacks = [tuple(integers(stack, 1)) for stack in _table(dictionary, 7)]
    assert stacks and stacks[0] == (), 'stack_table[0] must be empty (v4.5 schema required)'
    for stack in stacks:
        assert all(index < len(locations) for index in stack), 'invalid location index'
    attributes = []
    for fields in _table(dictionary, 6):
        values = _table(fields, 2)
        assert len(values) <= 1
        value = values[0] if values else []
        text = [b.decode('utf-8') for n, wire, b in value if n == 1 and wire == 2]
        assert len(text) <= 1
        attributes.append((strings[_scalar(fields, 1)], text[0] if text else ''))
    assert attributes and attributes[0] == ('', ''), 'attribute_table[0] must be empty'
    mappings = _table(dictionary, 1)
    assert mappings and mappings[0] == [], 'mapping_table[0] must be empty'
    links = _table(dictionary, 4)
    assert links, 'link_table[0] is required even when links are unused'
    assert next(value for n, wire, value in links[0] if n == 1 and wire == 2) == bytes(16)
    assert next(value for n, wire, value in links[0] if n == 2 and wire == 2) == bytes(8)

    profiles = [profile for resource in _table(top, 1)
                for scope in _table(resource, 2) for profile in _table(scope, 2)]
    result = []

    def value_type(profile, number):
        fields = _message(profile, number)
        return strings[_scalar(fields, 1)], strings[_scalar(fields, 2)]

    for profile in profiles:
        samples = []
        for fields in _table(profile, 2):
            stack_index = _scalar(fields, 1)
            assert stack_index < len(stacks), 'invalid stack index'
            stack = tuple(frame for index in stacks[stack_index] for frame in locations[index])
            attr_indices = integers(fields, 2)
            assert all(index < len(attributes) for index in attr_indices), 'invalid attribute index'
            attrs = tuple(sorted(attributes[index] for index in attr_indices
                                 if attributes[index] != ('', '')))
            times = []
            for n, wire, data in fields:
                if n != 5:
                    continue
                if wire == 2:
                    assert len(data) % 8 == 0, 'invalid packed fixed64 timestamps'
                    times.extend(value[0] for value in struct.iter_unpack('<Q', data))
                elif wire == 1:
                    times.append(struct.unpack('<Q', data)[0])
                else:
                    raise AssertionError('timestamp must use fixed64 wire encoding')
            values = integers(fields, 4)
            explicit = any(n == 4 for n, _, _ in fields)
            assert times and (not explicit or len(values) == len(times)), 'timestamp/value count mismatch'
            samples.append((stack, attrs, tuple(values if explicit else [1] * len(times)), tuple(times), explicit))
        result.append((_fixed64(profile, 3), _scalar(profile, 4), value_type(profile, 1),
                       value_type(profile, 5), _scalar(profile, 6), tuple(sorted(samples))))
    return tuple(result)


def otlp_totals(path):
    return sum(sum(sample[2]) for profile in semantic_otlp(path) for sample in profile[5])


if __name__ == '__main__':
    if len(sys.argv) not in (2, 3):
        raise SystemExit('Usage: otlp45.py JAVA.otlp [NATIVE.otlp]')
    first = semantic_otlp(sys.argv[1])
    same = len(sys.argv) == 2 or first == semantic_otlp(sys.argv[2])
    encoded = json.dumps(first, separators=(',', ':'), ensure_ascii=False).encode()
    print(json.dumps({'same': same, 'profiles': len(first),
                      'sample_groups': sum(len(p[5]) for p in first),
                      'events': sum(len(s[3]) for p in first for s in p[5]),
                      'canonical_sha256': hashlib.sha256(encoded).hexdigest()}, indent=2))
    if not same:
        raise SystemExit(1)
