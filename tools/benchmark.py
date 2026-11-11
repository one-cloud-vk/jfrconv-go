#!/usr/bin/env python3
"""Compare fresh-process peak RSS and whole-program p50/p95 conversion latency.

Both implementations get the same warmups, then alternate execution order for
all measured pairs. The first fresh-process pair is retained separately; file
page-cache state is uncontrolled and these are not claimed as cold-disk results.
Child wait4() RSS is bytes on macOS and KiB on Linux; Python-parent RSS excluded.

  python3 tools/benchmark.py --go target/release/jfrconv --java-jar old.jar \
      --input recording.jfr --repeat 21 --warmup 1
  python3 tools/benchmark.py ... --repeat-inputs 8 --go-flag=--max-rss \
      --go-flag=64M
"""
from __future__ import annotations
import argparse
from collections import Counter
import hashlib
import gzip
import json
import math
import os
from pathlib import Path
import platform
import statistics
import shutil
import subprocess
import sys
import tempfile
import time
import threading
from fixtures import generate


def file_hash(path):
    digest = hashlib.sha256()
    with path.open('rb') as source:
        for block in iter(lambda: source.read(64 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def freeze_input(source, pool, alias):
    """Copy once per content hash; aliases link only to our read-only pool."""
    expected = file_hash(source)
    pool.mkdir(parents=True, exist_ok=True)
    frozen = pool / expected
    if frozen.exists():
        if file_hash(frozen) != expected:
            raise AssertionError('Frozen input pool content changed')
    else:
        handle, temporary = tempfile.mkstemp(prefix='.input-', dir=pool)
        os.close(handle)
        temporary = Path(temporary)
        try:
            shutil.copyfile(source, temporary)
            if file_hash(temporary) != expected:
                raise AssertionError('Input changed while freezing')
            temporary.chmod(0o444)
            temporary.replace(frozen)
        finally:
            temporary.unlink(missing_ok=True)
    frozen.chmod(0o444)
    alias.parent.mkdir(parents=True, exist_ok=True)
    if alias.exists():
        if not alias.samefile(frozen):
            raise AssertionError('Refusing to replace an existing input alias')
    else:
        os.link(frozen, alias)
    return expected, frozen


def measure(command):
    with tempfile.TemporaryFile() as output:
        start = time.perf_counter()
        process = subprocess.Popen(list(map(str, command)), stdout=output, stderr=output)
        _, status, usage = os.wait4(process.pid, 0)
        elapsed = time.perf_counter() - start
        process.returncode = os.waitstatus_to_exitcode(status)
        output.seek(0)
        diagnostics = output.read().decode(errors='replace') if process.returncode else ''
    rss_bytes = usage.ru_maxrss if sys.platform == 'darwin' else usage.ru_maxrss * 1024
    result = {'peak_rss_bytes': rss_bytes, 'wall_seconds': elapsed,
            'user_cpu_seconds': usage.ru_utime, 'system_cpu_seconds': usage.ru_stime,
            'major_faults': usage.ru_majflt, 'minor_faults': usage.ru_minflt,
            'exit_code': process.returncode}
    if process.returncode:
        raise MeasurementError(f'{command}\n{diagnostics}', result)
    return result


class MeasurementError(RuntimeError):
    def __init__(self, message, result):
        super().__init__(message)
        self.result = result


TREE_RSS_METHOD = (
    'Separate instrumented RSS-only runs, excluded from latency percentiles. '
    'Sum simultaneous resident bytes of command PID and observed descendants; '
    'shared pages are counted per process. Linux reads procfs; macOS uses ps. '
    '2ms requested interval is best effort; short-lived children can be missed. '
    'Peak is a lower bound, not unique physical memory or cgroup memory.peak.'
)


def process_tree_rss(pid):
    if sys.platform == 'linux':
        page = os.sysconf('SC_PAGE_SIZE')
        total = 0
        pending = [pid]
        seen = set()
        while pending:
            current = pending.pop()
            if current in seen:
                continue
            seen.add(current)
            try:
                fields = Path(f'/proc/{current}/statm').read_text().split()
                total += int(fields[1]) * page
                # A Go worker can be forked from any runtime OS thread.
                for task in Path(f'/proc/{current}/task').iterdir():
                    try:
                        pending.extend(map(int, (task / 'children').read_text().split()))
                    except (FileNotFoundError, ProcessLookupError):
                        pass
            except FileNotFoundError:
                pass
            except ProcessLookupError:
                pass
        return total, len(seen)
    if sys.platform == 'darwin':
        result = subprocess.run(['ps', '-axo', 'pid=,ppid=,rss='], capture_output=True, text=True, check=True, timeout=2)
        processes = {}
        for line in result.stdout.splitlines():
            fields = line.split()
            if len(fields) == 3:
                child, parent, kib = map(int, fields)
                processes[child] = (parent, kib * 1024)
        selected = {pid}
        while True:
            added = {child for child, (parent, _) in processes.items() if parent in selected}
            if added <= selected:
                break
            selected |= added
        return sum(processes.get(p, (0, 0))[1] for p in selected), len(selected)
    raise RuntimeError('Process-tree RSS diagnostics support Linux and macOS')


def measure_tree_rss(command):
    stop = threading.Event()
    observations = []
    errors = []
    with tempfile.TemporaryFile() as output:
        process = subprocess.Popen(list(map(str, command)), stdout=output, stderr=output)
        def sample():
            while not stop.is_set():
                try:
                    resident, processes = process_tree_rss(process.pid)
                    observations.append((resident, processes))
                except Exception as error:
                    errors.append(str(error))
                    break
                stop.wait(.002)
        monitor = threading.Thread(target=sample, daemon=True)
        monitor.start()
        try:
            _, status, usage = os.wait4(process.pid, 0)
            process.returncode = os.waitstatus_to_exitcode(status)
        finally:
            stop.set()
            monitor.join(timeout=3)
        output.seek(0)
        diagnostics = output.read().decode(errors='replace') if process.returncode else ''
    command_highwater = usage.ru_maxrss if sys.platform == 'darwin' else usage.ru_maxrss * 1024
    peak = max([command_highwater, *[resident for resident, _ in observations]])
    result = {'sampled_tree_peak_lower_bound_bytes': peak,
            'command_wait4_highwater_bytes': command_highwater,
            'max_observed_processes': max([0, *[count for _, count in observations]]),
            'samples': len(observations), 'monitor_errors': errors,
            'complete': any(resident > 0 for resident, _ in observations) and not errors,
            'short_lived_children_may_be_missed': True,
            'included_in_latency_statistics': False, 'exit_code': process.returncode}
    if process.returncode:
        raise MeasurementError(f'{command}\n{diagnostics}', result)
    return result


def canonical(path):
    result = Counter()
    for line in path.read_text().splitlines():
        stack, value = line.rsplit(' ', 1)
        result[stack] += int(value)
    return +result


def p95(values):
    """Nearest-rank percentile; all raw measurements are retained."""
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * .95) - 1)]


def summarize(runs):
    rss = [r['peak_rss_bytes'] for r in runs]
    wall = [r['wall_seconds'] for r in runs]
    return {'median_peak_rss_bytes': statistics.median(rss),
            'p95_peak_rss_bytes': p95(rss), 'max_peak_rss_bytes': max(rss),
            'median_wall_seconds': statistics.median(wall),
            'p95_wall_seconds': p95(wall), 'max_wall_seconds': max(wall)}


def canonical_pprof(path):
    """Resolve IDs and retain vector values, labels, units and profile times.

    Producer comments and the producer-specific synthetic mapping name differ
    intentionally. They do not change profile samples or their interpretation.
    """
    from validate import protobuf, integers
    data = path.read_bytes()
    fields = protobuf(gzip.decompress(data) if data.startswith(b'\x1f\x8b') else data)
    strings = [v.decode('utf-8') for n, _, v in fields if n == 6]
    def scalar(message, number):
        return (integers(message, number) or [0])[0]
    functions = {}
    locations = {}
    for n, _, data in fields:
        if n == 5:
            f = protobuf(data)
            functions[scalar(f, 1)] = strings[scalar(f, 2)]
    for n, _, data in fields:
        if n == 4:
            f = protobuf(data)
            locations[scalar(f, 1)] = tuple(
                (functions[scalar(line, 1)], scalar(line, 2))
                for k, _, b in f if k == 4 for line in [protobuf(b)])
    samples = {}
    types = []
    for n, _, data in fields:
        if n == 1:
            f = protobuf(data)
            types.append((strings[scalar(f, 1)], strings[scalar(f, 2)]))
        elif n == 2:
            f = protobuf(data)
            stack = tuple(item for loc in integers(f, 1) for item in locations[loc])
            labels = []
            for k, _, b in f:
                if k == 3:
                    label = protobuf(b)
                    labels.append((strings[scalar(label, 1)], strings[scalar(label, 2)],
                                   scalar(label, 3), strings[scalar(label, 4)]))
            key = (stack, tuple(sorted(labels)))
            vector = tuple(integers(f, 2))
            previous = samples.get(key, (0,) * len(vector))
            if len(previous) != len(vector):
                raise AssertionError('pprof sample value vector length changed')
            samples[key] = tuple(a + b for a, b in zip(previous, vector))
    return (tuple(types), scalar(fields, 9), scalar(fields, 10),
            tuple(sorted(samples.items())))


def verify_outputs(args, source, outputs):
    proof = {}
    if args.format == 'collapsed':
        expected, actual = canonical(outputs['java']), canonical(outputs['go'])
    elif args.format in ['pprof', 'pb.gz', 'otlp']:
        from validate import semantic_otlp
        parse = semantic_otlp if args.format == 'otlp' else canonical_pprof
        expected, actual = parse(outputs['java']), parse(outputs['go'])
    elif args.format == 'heatmap':
        decoder = Path(__file__).with_name('heatmap_decode.js')
        decoded = {kind: json.loads(subprocess.check_output(['node', str(decoder), str(path)], text=True)) for kind, path in outputs.items()}
        expected, actual = decoded['java'], decoded['go']
        colors = Path(__file__).with_name('heatmap_colors.js')
        color_proof = json.loads(subprocess.check_output(
            ['node', str(colors), str(outputs['java']), str(outputs['go'])], text=True))
        if (not color_proof['same_used_method_IDs_RGB'] or
                color_proof['java_digest'] != color_proof['native_digest']):
            raise AssertionError(f'Heatmap used method ID/RGB mismatch for {source}; refusing benchmark comparison')
        proof['used_method_IDs_RGB_match'] = True
        proof['used_method_IDs_RGB_digest'] = color_proof['java_digest']
        proof['used_methods'] = color_proof['java_methods']
    elif args.format == 'html':
        converted = {}
        for kind, path in outputs.items():
            converted[kind] = args.workdir / (source.stem + '.' + kind + '.html-check.collapsed')
            converted[kind].unlink(missing_ok=True)
            subprocess.run([args.java, *args.java_option, '-jar', str(args.java_jar), '-o', 'collapsed', str(path), str(converted[kind])], capture_output=True, check=True)
        expected, actual = canonical(converted['java']), canonical(converted['go'])
    else:
        raise ValueError('Unsupported semantic output verification format')
    if expected != actual:
        raise AssertionError(f'Output mismatch for {source}; refusing benchmark comparison')
    if isinstance(actual, Counter):
        return {'output_matches': True, 'output_total': sum(actual.values()), 'output_stacks': len(actual)}
    return {'output_matches': True, **proof}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', type=Path, required=True)
    parser.add_argument('--go-tool', default='go', help='Go tool used only to read immutable binary build metadata')
    parser.add_argument('--tree-rss-runs', type=int, default=0, help='additional instrumented RSS-only diagnostic pairs; never included in latency percentiles')
    parser.add_argument('--java-jar', type=Path, required=True)
    parser.add_argument('--java', default='java')
    parser.add_argument('--java-option', action='append', default=[], help='e.g. --java-option=-Xmx256m')
    parser.add_argument('--input', type=Path, action='append', default=[])
    parser.add_argument('--input-pool', type=Path, help='shared content-addressed read-only input pool; case aliases link only to this trusted pool')
    parser.add_argument('--repeat', type=int, default=21)
    parser.add_argument('--warmup', type=int, default=1)
    parser.add_argument('--repeat-inputs', type=int, default=1, help='convert the same input N times in each fresh process, to check sustained memory')
    parser.add_argument('--samples', type=int, nargs='+', default=[100_000, 2_000_000])
    parser.add_argument('--chunks', type=int, default=4)
    parser.add_argument('--stacks', type=int, default=3)
    parser.add_argument('--event-mode', choices=['cpu', 'all', 'wall', 'alloc', 'lock'], default='cpu')
    parser.add_argument('--format', choices=['collapsed', 'html', 'pprof', 'pb.gz', 'otlp', 'heatmap'], default='collapsed')
    parser.add_argument('--flag', action='append', default=[], help='converter flag passed to both implementations')
    parser.add_argument('--go-flag', action='append', default=[], help='additional Go-only flag, e.g. --go-flag=--max-rss')
    parser.add_argument('--workdir', type=Path, default=Path('work/benchmark'))
    args = parser.parse_args()
    if args.repeat <= 0 or args.warmup < 0 or args.repeat_inputs <= 0 or args.tree_rss_runs < 0:
        parser.error('--repeat and --repeat-inputs must be positive; --warmup nonnegative')
    args.go = args.go.resolve(); args.java_jar = args.java_jar.resolve()
    args.workdir.mkdir(parents=True, exist_ok=True)
    # Rebuilds in another agent/process cannot silently mix executables within a
    # report. Measure immutable copies and record both original source paths.
    binary_source, jar_source = args.go, args.java_jar
    measured_binary = (args.workdir / 'jfrconv-measured').resolve()
    measured_jar = (args.workdir / 'java-baseline.jar').resolve()
    if binary_source != measured_binary:
        shutil.copyfile(binary_source, measured_binary)
        measured_binary.chmod(binary_source.stat().st_mode & 0o777)
    if jar_source != measured_jar:
        shutil.copyfile(jar_source, measured_jar)
    args.go, args.java_jar = measured_binary, measured_jar
    input_pool = (args.input_pool or args.workdir / 'input-pool').resolve()
    inputs = args.input or [generate(args.workdir / f'{n}-samples.jfr', n, args.chunks, args.stacks) for n in args.samples]
    mode_flags = [] if args.event_mode == 'all' else ['--' + args.event_mode]
    rows = []
    report = {'command': sys.argv, 'go_binary_source': str(binary_source), 'java_jar_source': str(jar_source),
              'implementation': 'native Go', 'status': 'running',
              'go_build_info': subprocess.run([args.go_tool, 'version', '-m', str(args.go)], capture_output=True, text=True).stdout.strip(),
              'go_runtime_environment': {key: os.environ.get(key) for key in ['GOGC','GOMEMLIMIT','GOMAXPROCS','GODEBUG']},
              'tree_rss_diagnostic_pairs': args.tree_rss_runs,
              'tree_rss_method': TREE_RSS_METHOD if args.tree_rss_runs else None,
              'frozen_input_pool': str(input_pool),
              'java_version': subprocess.run([args.java, '-version'], capture_output=True, text=True).stderr.strip(),
              'hardware': {'machine': platform.machine(), 'processor': platform.processor(), 'logical_cpus': os.cpu_count()},
              'go_binary_sha256': file_hash(args.go), 'java_jar_sha256': file_hash(args.java_jar),
              'platform': platform.platform(), 'python': platform.python_version(), 'repeat': args.repeat,
              'warmups_per_implementation': args.warmup, 'format': args.format,
              'java_options': args.java_option, 'flags': [*mode_flags, *args.flag], 'go_flags': args.go_flag,
              'method': 'Fresh processes measured with wait4 child ru_maxrss; equal warmups; alternating pair order; p50 median and p95 nearest-rank; first fresh pair kept separate; input page-cache state uncontrolled; macOS RSS bytes, Linux KiB normalized to bytes.',
              'output_verification_method': 'Canonical output after every pair outside measurement; pprof includes vector values, resolved labels, sample types/units and time/duration, with producer comments/mapping title excluded; heatmap includes all used method IDs and RGB.',
              'recordings': rows}
    destination = args.workdir / 'benchmark.json'
    def persist():
        pending = destination.with_suffix('.json.tmp')
        pending.write_text(json.dumps(report, indent=2) + '\n')
        pending.replace(destination)
    persist()
    try:
        for source_index, original_source in enumerate(inputs):
            # One immutable physical copy per SHA256 across the whole matrix.
            # Per-case aliases never link to mutable original fixture files.
            source = (args.workdir / 'inputs' / str(source_index) / original_source.name).absolute()
            input_sha, frozen = freeze_input(original_source, input_pool, source)
            row = {'input_source': str(original_source), 'input': str(source),
                   'input_pool_file': str(frozen),
                   'input_bytes': source.stat().st_size,
                   'input_sha256': input_sha, 'input_count_per_process': args.repeat_inputs,
                   'processed_input_bytes_per_process': source.stat().st_size * args.repeat_inputs,
                   'runs': {'java': [], 'go': []}, 'commands': {},
                   'first_fresh_process': {}, 'warmup_runs': {'java': [], 'go': []},
                   'output_verification': {'warmup_pairs': [], 'measured_pairs': [], 'tree_rss_pairs': []}}
            rows.append(row)
            outputs = {}
            for kind in ['java', 'go']:
                output = args.workdir / (source.stem + '.' + kind + '.' + args.format)
                target = output
                if args.repeat_inputs > 1:
                    target = args.workdir / (source.stem + '.' + kind + '.outputs')
                    target.mkdir(exist_ok=True)
                    output = target / (source.stem + '.' + ('html' if args.format == 'heatmap' else args.format))
                command = ([args.java, *args.java_option, '-jar', str(args.java_jar)] if kind == 'java' else [str(args.go), *args.go_flag])
                command += [*mode_flags, *args.flag, '-o', args.format, *([str(source)] * args.repeat_inputs), str(target)]
                row['commands'][kind] = command
                outputs[kind] = output
            def trial(kind, collection, phase, iteration=None, tree=False):
                # A successful no-write command must never validate stale data.
                outputs[kind].unlink(missing_ok=True)
                report['active_trial'] = {'input_index': source_index, 'phase': phase,
                                          'iteration': iteration, 'implementation': kind}
                persist()
                try:
                    result = (measure_tree_rss if tree else measure)(row['commands'][kind])
                except MeasurementError as error:
                    result = error.result
                    if iteration is None:
                        collection[kind] = result
                    else:
                        collection[kind].append(result)
                    persist()
                    raise
                if iteration is None:
                    collection[kind] = result
                else:
                    collection[kind].append(result)
                persist()
            def verify(phase, iteration=None):
                report['active_trial'] = {'input_index': source_index, 'phase': phase + '-verification',
                                          'iteration': iteration}
                persist()
                proof = verify_outputs(args, source, outputs)
                if iteration is None:
                    row['output_verification'][phase] = proof
                else:
                    row['output_verification'][phase].append(proof)
                persist()
                return proof
            first_order = ['java', 'go'] if source_index % 2 == 0 else ['go', 'java']
            for kind in first_order:
                trial(kind, row['first_fresh_process'], 'first_fresh_process')
            verify('first_fresh_process')
            for iteration in range(args.warmup):
                order = first_order if iteration % 2 == 0 else list(reversed(first_order))
                for kind in order:
                    trial(kind, row['warmup_runs'], 'warmup', iteration)
                verify('warmup_pairs', iteration)
            row['execution_order'] = []
            for iteration in range(args.repeat):
                order = first_order if iteration % 2 == 0 else list(reversed(first_order))
                row['execution_order'].append(order)
                for kind in order:
                    trial(kind, row['runs'], 'measured', iteration)
                row.update(verify('measured_pairs', iteration))
            if args.tree_rss_runs:
                row['tree_rss_diagnostics'] = {'java': [], 'go': []}
                for iteration in range(args.tree_rss_runs):
                    order = first_order if iteration % 2 == 0 else list(reversed(first_order))
                    for kind in order:
                        trial(kind, row['tree_rss_diagnostics'], 'tree_rss', iteration, tree=True)
                    verify('tree_rss_pairs', iteration)
            if file_hash(source) != row['input_sha256']:
                raise AssertionError('Frozen input changed during benchmark')
            for kind in ['java', 'go']:
                row[kind] = summarize(row['runs'][kind])
            java_rss, go_rss = row['java']['median_peak_rss_bytes'], row['go']['median_peak_rss_bytes']
            row['rss_reduction_percent'] = 100 * (1 - go_rss / java_rss)
            row['latency_p50_speedup_ratio'] = row['java']['median_wall_seconds'] / row['go']['median_wall_seconds']
            row['latency_p95_speedup_ratio'] = row['java']['p95_wall_seconds'] / row['go']['p95_wall_seconds']
            persist()
            print(f'{source.name} x{args.repeat_inputs}: RSS Java {java_rss / 1048576:.2f} MiB vs Go {go_rss / 1048576:.2f} MiB ({row["rss_reduction_percent"]:.1f}% lower); p50 {row["java"]["median_wall_seconds"]:.4f}s vs {row["go"]["median_wall_seconds"]:.4f}s; p95 {row["java"]["p95_wall_seconds"]:.4f}s vs {row["go"]["p95_wall_seconds"]:.4f}s', flush=True)
        report['status'] = 'complete'
        report.pop('active_trial', None)
    except BaseException as error:
        report['status'] = 'failed'
        report['error'] = {'type': type(error).__name__, 'message': str(error)}
        raise
    finally:
        persist()
    print(destination)


if __name__ == '__main__':
    main()
