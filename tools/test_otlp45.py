"""Regression checks for the independent v4.5 benchmark acceptance decoder."""
from pathlib import Path
import struct
import tempfile
from types import SimpleNamespace
import unittest

from otlp45 import semantic_otlp, otlp_totals
from protobuf_wire import protobuf
from benchmark import verify_outputs


def v(number):
    out = bytearray()
    while number >= 128:
        out.append((number & 127) | 128)
        number >>= 7
    out.append(number)
    return bytes(out)


def integer(tag, number):
    return v(tag << 3) + v(number)


def message(tag, data):
    return v(tag << 3 | 2) + v(len(data)) + data


def recording(times=(1100, 1200), values=(2, 3), *, stack_table=True):
    strings = ['', 'cpu', 'count', 'nanoseconds', 'thread.name', 'leaf']
    dictionary = message(1, b'') + message(4, message(1, bytes(16)) + message(2, bytes(8)))
    dictionary += message(3, integer(1, 0)) + message(3, integer(1, 5))
    dictionary += message(2, b'') + message(2, integer(1, 0) + message(3, integer(1, 1) + integer(2, 17)))
    if stack_table:
        dictionary += message(7, message(1, b'')) + message(7, message(1, v(1)))
    dictionary += b''.join(message(5, text.encode()) for text in strings)
    dictionary += message(6, b'') + message(6, integer(1, 4) + message(2, message(1, b'worker')))
    sample = integer(1, 1) + integer(2, 1) + message(5, b''.join(struct.pack('<Q', t) for t in times))
    if values is not None:
        sample += message(4, b''.join(v(x) for x in values))
    profile = message(1, integer(1, 1) + integer(2, 2))
    profile += message(5, integer(1, 1) + integer(2, 3)) + integer(6, 20000000)
    profile += v(3 << 3 | 1) + struct.pack('<Q', 1000) + integer(4, 400) + message(2, sample)
    return message(1, message(2, message(2, profile))) + message(2, dictionary)


class Otlp45AcceptanceTest(unittest.TestCase):
    def decode(self, data):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'profile.otlp'
            path.write_bytes(data)
            return semantic_otlp(path)

    def test_resolves_global_dictionary_and_all_event_pairs(self):
        result = self.decode(recording())
        self.assertEqual(result[0][:5], (1000, 400, ('cpu', 'count'), ('cpu', 'nanoseconds'), 20000000))
        self.assertEqual(result[0][5], (((('leaf', 17),), (('thread.name', 'worker'),), (2, 3), (1100, 1200), True),))

    def test_equal_totals_do_not_hide_changed_timestamps_or_values(self):
        reference = self.decode(recording())
        self.assertNotEqual(reference, self.decode(recording(times=(1100, 1201))))
        self.assertNotEqual(reference, self.decode(recording(values=(1, 4))))
        self.assertNotEqual(reference, self.decode(recording(times=(1200, 1100), values=(3, 2))))

    def test_implicit_one_representation_and_totals(self):
        implicit = self.decode(recording(values=None))
        self.assertEqual(implicit[0][5][0][2:], ((1, 1), (1100, 1200), False))
        self.assertNotEqual(implicit, self.decode(recording(values=(1, 1))))
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'profile.otlp'
            path.write_bytes(recording(values=None))
            self.assertEqual(otlp_totals(path), 2)

    def test_rejects_missing_v45_stack_table_and_mismatched_arrays(self):
        with self.assertRaises(AssertionError):
            self.decode(recording(stack_table=False))
        with self.assertRaises(AssertionError):
            self.decode(recording(values=(5,)))

    def test_wire_reader_rejects_truncation_and_overflow(self):
        for data in (b'\x0a\x03x', b'\x19\0', b'\x08' + b'\xff' * 9 + b'\x02'):
            with self.subTest(data=data):
                with self.assertRaises(ValueError):
                    protobuf(data)

    def test_benchmark_refuses_equal_total_with_changed_event_time(self):
        with tempfile.TemporaryDirectory() as directory:
            paths = {kind: Path(directory) / (kind + '.otlp') for kind in ('java', 'go')}
            paths['java'].write_bytes(recording())
            paths['go'].write_bytes(recording(times=(1100, 1201)))
            with self.assertRaisesRegex(AssertionError, 'refusing benchmark'):
                verify_outputs(SimpleNamespace(format='otlp'), Path('fixture.jfr'), paths)
            paths['go'].write_bytes(recording())
            proof = verify_outputs(SimpleNamespace(format='otlp'), Path('fixture.jfr'), paths)
            self.assertTrue(proof['otlp_full_timestamp_value_pairs_match'])
            self.assertEqual(proof['otlp_event_timestamps'], 2)


if __name__ == '__main__':
    unittest.main()
