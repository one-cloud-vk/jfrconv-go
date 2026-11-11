#!/usr/bin/env python3
"""Verify frozen source/binary provenance and package native Linux evidence."""
from pathlib import Path
import hashlib
import json
import re
import shutil

root = Path(__file__).resolve().parents[2]
work = root / 'work/upstream-linux'
results = work / 'final-native-results'
digest = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
sources = json.loads((results / 'source-before.json').read_text())
for name, source in sources.items():
    for directory in (Path(source['snapshot']), Path(source['project'])):
        actual = {filename: digest(directory / filename) for filename in source['files']}
        assert actual == source['files'], f'{name} source changed: {directory}'
        parts = ('Cargo.toml', 'Cargo.lock', 'src', 'assets', 'tests', 'tools') if name == 'rust' else ('go.mod', 'cmd', 'internal', 'assets', 'tests', 'tools')
        present = set()
        for part in parts:
            path = directory / part
            candidates = path.rglob('*') if path.is_dir() else (path,)
            present.update(str(p.relative_to(directory)) for p in candidates if p.is_file() and '__pycache__' not in p.parts and p.suffix != '.pyc')
        assert present == set(source['files']), f'{name} file list changed: {directory}'
statuses = json.loads((results / 'exit-status.json').read_text())
assert statuses and all(value == 0 for value in statuses.values()), statuses
cgroup = json.loads((results / 'cgroup-report.json').read_text())
assert cgroup['returncode'] == 0 and cgroup['all_checks_passed'] and cgroup['configured_memory_max_bytes'] == 64 << 20, cgroup
go_bins = json.loads((results / 'go-binaries-before.json').read_text())
assert all(digest(work / 'final-go-bin' / name) == sha for name, sha in go_bins.items())
binary_lines = (results / 'linux-binaries.sha256').read_text().splitlines()
linux_bins = {Path(line.split()[1]).name + ('-rust' if 'linux-target' in line else '-go'): line.split()[0] for line in binary_lines}
assert linux_bins['jfrconv-rust'] == digest(work / 'linux-target/release/jfrconv')
assert linux_bins['jfrconv-go'] == go_bins['jfrconv']
rust_log = (results / 'cargo-tests.log').read_text()
totals = re.findall(r'test result: ok\. (\d+) passed; (\d+) failed; (\d+) ignored;', rust_log)
rust_counts = {key: sum(int(row[index]) for row in totals) for index, key in enumerate(('passed', 'failed', 'ignored'))}
assert rust_counts == {'passed': 78, 'failed': 0, 'ignored': 0}, rust_counts
go_counts = {}
for package in ('jfr', 'convert', 'cli'):
    source = Path(sources['go']['snapshot']) / 'internal' / package
    expected = {name for path in source.glob('*test.go') for name in re.findall(r'^func (Test\w+)\(t \*testing.T\)', path.read_text(), re.M)}
    log = (results / f'go-{package}-tests.log').read_text()
    rows = re.findall(r'^--- (PASS|FAIL|SKIP): (Test\w+) \(', log, re.M)
    assert {name for _, name in rows} == expected, (package, expected, rows)
    assert not any(status == 'FAIL' for status, _ in rows) and log.rstrip().endswith('PASS'), package
    go_counts[package] = {status.lower(): sum(1 for value, _ in rows if value == status) for status in ('PASS', 'FAIL', 'SKIP')}
    go_counts[package]['skipped_tests'] = [name for status, name in rows if status == 'SKIP']
    assert all(name in {'TestStdoutAliasPreservesRegularFilePrefixInBothModes', 'TestNullDeviceIdentityInBothModes'} for name in go_counts[package]['skipped_tests']), (package, rows)
public = {}
for kind in ('java', 'rust', 'go'):
    stage = work / ('v41-' + kind)
    manifest = json.loads((stage / 'public-test-manifest.json').read_text())
    assert all(digest(stage / name) == sha for name, sha in manifest['assertion_sources'].items()), kind
    report = json.loads((stage / 'public-test-results/public-tests.json').read_text())
    assert report['returncode'] == 0 and report['counts'] == {'PASS': 1} and report['assertion_files_unchanged'], report
    if kind != 'java':
        assert report[kind + '_binary_sha256'] == linux_bins['jfrconv-' + kind], report
    public[kind] = report
summary = {
    'status': 'VERIFIED',
    'platform': 'Linux arm64, native execution in disposable default-security Docker container',
    'security': 'No privileged mode, added capabilities, seccomp override, or user image changes. Compilers installed only in task-owned ephemeral tmpfs.',
    'source_manifests': sources,
    'linux_binaries_sha256': linux_bins,
    'rust_version': '0.2.2',
    'rust_tests': rust_counts,
    'rust_explicit_root_probe_skips': re.findall(r'SKIP[^\n]+', rust_log),
    'go_tests': go_counts,
    'go_test_note': 'Host-crosscompiled with official Go 1.27.4 CGO_ENABLED=0; executables ran natively on Linux. Recorded JDK/AP fixtures, original JavaScript heatmap oracle, actual 64-file batch, soft budget/RSS supervision, task-owned FIFO and file descriptor tests executed. Two root-only system device probe tests intentionally skip; task-owned descriptor/FIFO tests still execute.',
    'public_tests': public,
    'exit_status': statuses,
    'strict_whole_tree_cgroup': cgroup,
}
(results / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
for kind, folder in (('rust', root / 'outputs/jfrconv-rs/reports/linux/v0.2.2'), ('go', root / 'outputs/jfrconv-go/reports/linux')):
    folder.mkdir(parents=True, exist_ok=True)
    for path in sorted(results.iterdir()):
        if path.is_file():
            shutil.copy2(path, folder / path.name)
    for script in ('prepare-final-native.py', 'final-native-run.sh', 'launch_final_native.py', 'check-final-cgroup.py', 'package-final-native.py'):
        shutil.copy2(work / script, folder / script)
    for implementation in ('java', 'rust', 'go'):
        stage = work / ('v41-' + implementation)
        shutil.copy2(stage / 'public-test-manifest.json', folder / (implementation + '-public-manifest.json'))
        for filename in ('public-tests.json', 'tests.log', 'build.log'):
            path = stage / 'public-test-results' / filename
            if path.exists():
                shutil.copy2(path, folder / (implementation + '-' + filename))
print(json.dumps({'rust': rust_counts, 'go': go_counts, 'public': {kind: value['counts'] for kind, value in public.items()}, 'binaries': linux_bins}, indent=2))
