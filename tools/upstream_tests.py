#!/usr/bin/env python3
"""Run the pinned async-profiler 4.5 converter and Span API tests."""
from __future__ import annotations

import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import time

COMMIT = '11aaea310cb75843f0e7532365d8cedc8d0e14c8'
PROTOBUF_SHA256 = 'd60dfe7c68a0d38a248cca96924f289dc7e1966a887ee7cae397701af08575ae'
HELPERS = Path(__file__).resolve().with_name('upstream')
CLASSPATH = os.pathsep.join(['build/test.jar', 'build/jar/*', 'build/lib/*',
                             'test/deps/*', 'test/gen/*'])
FILTERS = [
    'JfrTests.cpuLoad', 'JfrTests.recordCpuMultiEngine', 'JfrTests.ttsp',
    'StackwalkerTests', 'NativememTests.ldpreload',
    'NativelockTests.ldpreloadAllLockTypes', 'NativelockTests.nativeAllLockContention',
    'JfrconverterTests', 'OtlpTests.periodFromJfr', 'OtlpTests.threadNameFromJfr',
    'OtlpTests.samplesFromJfr', 'OtlpTests.nonAggregatedSamplesFromJfr',
    'OtlpTests.profileTimeFromJfr', 'SpanTests',
]


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')


def assertions(stage):
    return {str(path.relative_to(stage)): digest(path)
            for path in sorted((stage / 'test/test').rglob('*.java'))}


def replace_body(source, signature, body):
    """Replace a Java method body without treating comments/strings as braces."""
    opening = source.index('{', source.index(signature))
    depth, index, quote, comment = 1, opening + 1, None, None
    while depth:
        char, pair = source[index], source[index:index + 2]
        if comment == '//':
            if char == '\n':
                comment = None
        elif comment == '/*':
            if pair == '*/':
                comment = None
                index += 1
        elif quote:
            if char == '\\':
                index += 1
            elif char == quote:
                quote = None
        elif pair in ('//', '/*'):
            comment = pair
            index += 1
        elif char in ('"', "'"):
            quote = char
        elif char == '{':
            depth += 1
        elif char == '}':
            depth -= 1
        index += 1
    return source[:opening + 1] + '\n' + body + '\n    ' + source[index - 1:]


def install_bridge(stage):
    shutil.copyfile(HELPERS / 'GoBridge.java', stage / 'src/converter/one/convert/GoBridge.java')

    def patch(relative, signature, body):
        path = stage / relative
        path.write_text(replace_body(path.read_text(), signature, body))

    patch('test/one/profiler/test/Output.java', 'public static Output convertJfrToCollapsed(',
          '        return new Output(new String(one.convert.GoBridge.collapsed(input, args), '
          '"UTF-8").split(System.lineSeparator()));')
    for name, format_expression in [('JfrToFlame', '"collapsed".equals(args.output) ? "collapsed" : "html"'),
                                    ('JfrToHeatmap', '"heatmap"')]:
        patch(f'src/converter/one/convert/{name}.java',
              'public static void convert(String input, String output, Arguments args)',
              f'        GoBridge.convert(input, output, args, {format_expression});')
    patch('src/converter/one/convert/Main.java', 'public static void main(String[] argv)',
          '        GoBridge.main(argv);')
    patch('src/converter/one/convert/JfrToOtlp.java', 'public void dump(OutputStream out)',
          '        GoBridge.dumpOtlp(jfr, args, out);')
    reader = stage / 'src/converter/one/jfr/JfrReader.java'
    text = reader.read_text()
    constructor = '    public JfrReader(String fileName) throws IOException {\n'
    if text.count(constructor) != 1:
        raise ValueError('Expected the pinned filename-backed JfrReader constructor')
    reader.write_text(text.replace(constructor, constructor +
                      '        one.convert.GoBridge.register(this, fileName);\n', 1))


def prepare(args):
    source, stage = args.source.resolve(), args.dest.resolve()
    if stage.exists():
        raise SystemExit(f'Refusing to replace existing directory: {stage}')
    commit = subprocess.check_output(['git', '-C', str(source), 'rev-parse', COMMIT + '^{commit}'],
                                     text=True).strip()
    if commit != COMMIT:
        raise ValueError('The async-profiler 4.5 commit is unavailable')
    stage.mkdir(parents=True)
    with tempfile.TemporaryFile() as archive:
        subprocess.run(['git', '-C', str(source), 'archive', COMMIT], stdout=archive, check=True)
        archive.seek(0)
        with tarfile.open(fileobj=archive) as contents:
            contents.extractall(stage, filter='data')
    expected = assertions(stage)
    if not args.java_only:
        install_bridge(stage)
    if assertions(stage) != expected:
        raise AssertionError('Published assertion sources changed while staging')
    if args.protobuf_jar:
        if digest(args.protobuf_jar) != PROTOBUF_SHA256:
            raise ValueError('Expected protobuf-java 4.31.1')
        destination = stage / 'test/deps/protobuf-java-4.31.1.jar'
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(args.protobuf_jar, destination)
    write_json(stage / 'public-test-manifest.json', {
        'upstream_commit': COMMIT, 'native_transport': not args.java_only,
        'assertion_sources': expected,
    })
    print(stage)


def latency_check(java, jar, recording, native, minimum_ms, output):
    """The upstream latency visitor is Java-only; also check native output."""
    output.mkdir()
    maps = {}
    for name, command in [('java', [java, '-jar', str(jar)]), ('native', [str(native)])]:
        target = output / (name + '.collapsed')
        command += ['--wall', '--latency', f'{minimum_ms}ms', '-o', 'collapsed',
                    str(recording), str(target)]
        completed = subprocess.run(command, capture_output=True, text=True)
        (output / (name + '.stdout.log')).write_text(completed.stdout)
        (output / (name + '.stderr.log')).write_text(completed.stderr)
        completed.check_returncode()
        values = Counter()
        for line in target.read_text().splitlines():
            stack, count = line.rsplit(' ', 1)
            values[stack] += int(count)
        maps[name] = values
    native_map = maps['native']
    conditions = {
        'nonempty': bool(native_map),
        'showcase1_present': any('test/jfrconverter/Tracer.showcase1' in stack for stack in native_map),
        'showcase2_present': any('test/jfrconverter/Tracer.showcase2' in stack for stack in native_map),
        'showcase3_absent': not any('test/jfrconverter/Tracer.showcase3' in stack for stack in native_map),
    }
    result = {'input_sha256': digest(recording), 'minimum_ms': minimum_ms,
              'same_stack_counts': maps['java'] == native_map, 'conditions': conditions}
    result['passed'] = result['same_stack_counts'] and all(conditions.values())
    write_json(output / 'report.json', result)
    return result


def run(args):
    stage, output = args.stage.resolve(), args.output.resolve()
    manifest = json.loads((stage / 'public-test-manifest.json').read_text())
    if manifest['upstream_commit'] != COMMIT:
        raise ValueError('Expected a stage prepared from async-profiler 4.5')
    if assertions(stage) != manifest['assertion_sources']:
        raise AssertionError('Published assertion sources changed')
    native = args.native.resolve() if args.native else None
    reference = args.reference_jar.resolve() if args.reference_jar else None
    if manifest['native_transport'] and (native is None or reference is None):
        raise SystemExit('Native staging requires --native and a pristine 4.5 --reference-jar')
    if not manifest['native_transport'] and native is not None:
        raise SystemExit('--native requires a stage prepared without --java-only')
    protobuf = stage / 'test/deps/protobuf-java-4.31.1.jar'
    if not protobuf.exists() or digest(protobuf) != PROTOBUF_SHA256:
        raise SystemExit('Install protobuf-java 4.31.1 with prepare --protobuf-jar; see TESTING.md')
    output.mkdir(parents=True, exist_ok=False)
    if not args.no_build:
        with (output / 'build.log').open('w') as log:
            subprocess.run(['make', '-j2', 'all', 'build-test-java'], cwd=stage,
                           stdout=log, stderr=subprocess.STDOUT, check=True)
    scope_dir = output / 'scope'
    scope_dir.mkdir()
    subprocess.run([args.javac, '-cp', CLASSPATH, '-d', str(scope_dir),
                    str(HELPERS / 'PublicScope.java')], cwd=stage, check=True)
    scope = subprocess.check_output([args.java, '-cp', str(scope_dir) + os.pathsep + CLASSPATH,
                                     'PublicScope', *FILTERS], cwd=stage, text=True)
    (output / 'scope.tsv').write_text(scope)
    if len(scope.splitlines()) != 27:
        raise AssertionError('Expected 27 published test declarations')
    hashes = {str(path): digest(path) for path in (native, reference) if path}
    command = [args.java, '-ea', '-Djava.library.path=build/test/lib', '-DthreadCount=1',
               '-DretryCount=0', '-DlogDir=' + str(output / 'profiles'),
               '-cp', CLASSPATH, 'one.profiler.test.Runner', *FILTERS]
    environment = dict(os.environ)
    if native:
        environment['JFRCONV_BINARY'] = str(native)
    started = time.perf_counter()
    with (output / 'tests.log').open('w') as log:
        completed = subprocess.run(command, cwd=stage, env=environment,
                                   stdout=log, stderr=subprocess.STDOUT)
    rows = []
    for line in (output / 'tests.log').read_text(errors='replace').splitlines():
        match = re.match(r'^(PASS|FAIL|SKIP_[A-Z_]+) \[(\d+)/(\d+)\] (.*) took ([0-9.,]+) s$', line)
        if match:
            rows.append({'status': match[1], 'test': match[4],
                         'seconds': float(match[5].replace(',', '.'))})
    counts = dict(Counter(row['status'] for row in rows))
    report = {'upstream_commit': COMMIT, 'native_transport': bool(native),
              'command': command, 'binary_hashes': hashes, 'counts': counts, 'tests': rows,
              'elapsed_seconds': time.perf_counter() - started,
              'returncode': completed.returncode}
    status = completed.returncode
    if len(rows) != 27 or assertions(stage) != manifest['assertion_sources']:
        status = 1
    if native and status == 0:
        tracer = (stage / 'test/test/jfrconverter/Tracer.java').read_text()
        minimum = int(re.search(r'TRACE_DURATION_MS\s*=\s*(\d+)', tracer)[1]) - 10
        recordings = list((output / 'profiles').glob('JfrconverterTests.latencyFilter*/f.jfr'))
        if len(recordings) != 1:
            raise AssertionError('Expected the latencyFilter recording')
        result = latency_check(args.java, reference, recordings[0], native, minimum,
                               output / 'latency')
        report['native_latency_check'] = result
        if not result['passed']:
            status = 1
    if any(digest(path) != expected for path, expected in hashes.items()):
        raise AssertionError('A measured executable changed during the run')
    report['effective_returncode'] = status
    write_json(output / 'public-tests.json', report)
    print(json.dumps({'counts': counts, 'returncode': status,
                      'report': str(output / 'public-tests.json')}, indent=2))
    return status


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest='action', required=True)
    prepare_parser = actions.add_parser('prepare', help='archive the pinned source and install test transport')
    prepare_parser.add_argument('--source', type=Path, required=True, help='async-profiler Git checkout')
    prepare_parser.add_argument('--dest', type=Path, required=True)
    prepare_parser.add_argument('--protobuf-jar', type=Path)
    prepare_parser.add_argument('--java-only', action='store_true', help='stage the unmodified Java baseline')
    run_parser = actions.add_parser('run', help='build and execute all 27 declarations')
    run_parser.add_argument('--stage', type=Path, required=True)
    run_parser.add_argument('--output', type=Path, required=True, help='new results directory')
    run_parser.add_argument('--native', type=Path)
    run_parser.add_argument('--reference-jar', type=Path)
    run_parser.add_argument('--java', default='java')
    run_parser.add_argument('--javac', default='javac')
    run_parser.add_argument('--no-build', action='store_true')
    args = parser.parse_args()
    return prepare(args) if args.action == 'prepare' else run(args)


if __name__ == '__main__':
    raise SystemExit(main())
