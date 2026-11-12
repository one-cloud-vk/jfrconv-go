#!/usr/bin/env python3
"""Portable, post-measurement adapter for the pinned deterministic fixture writers."""
from pathlib import Path
import argparse
import hashlib
import json
import shutil
import fixtures

BASE = Path(__file__).resolve().parent
NAMES = {
    'cpu-100k': '100000-samples.jfr', 'cpu-10m': '10000000-samples.jfr',
    'cpu-diverse-2m': '2000000-high-cardinality.jfr',
    'deep-shared-cp': 'deep-shared-cp.jfr', 'cpu-2m': '2000000-samples.jfr',
    'span-api-real': 'span-api45.jfr', 'span-stress': 'span-stress45.jfr',
}


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for part in iter(lambda: f.read(65536), b''): h.update(part)
    return h.hexdigest()


def stress(path):
    # Identical events/ordering/metadata to original-generate_span_stress.py.
    from fixtures import EXEC, SPAN, TICKS, record, values, write_chunk
    with path.open('wb') as output:
        for chunk in range(4):
            base = TICKS * (chunk + 1)
            events = [record(EXEC, values(base + j * 2000, 1 + j % 2, 1 + j % 3, 0)) for j in range(500000)]
            events.extend(record(SPAN, values(base + j * 40000, 20000, 1 + (j // 2) % 2, 1 + j % 2)) for j in range(25000))
            if chunk: events.append(record(SPAN, values(base - 100000, 200000, 1, 1)))
            write_chunk(output, events, label='stress' + str(chunk), chunk_index=chunk, span_current=True, tag_strings=['selected', 'other'])


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--output', required=True, type=Path)
    p.add_argument('--dataset', choices=list(NAMES), action='append')
    a = p.parse_args(); a.output.mkdir(parents=True, exist_ok=True)
    plan = json.loads((BASE.parent / 'plan.json').read_text())
    for name in a.dataset or NAMES:
        target = a.output / NAMES[name]; expected = plan['datasets'][name]
        if not target.exists():
            if name == 'span-api-real': shutil.copyfile(BASE / 'span-api45.jfr', target)
            elif name == 'span-stress': stress(target)
            elif name == 'deep-shared-cp': fixtures.generate_deep_shared(target, chunks=2)
            else: fixtures.generate(target, samples=expected['samples'], chunks=expected['chunks'], stacks=expected['stacks'])
        assert target.stat().st_size == expected['bytes'], name
        assert digest(target) == expected['sha256'], name
        print(name, target, expected['sha256'])


if __name__ == '__main__': main()
