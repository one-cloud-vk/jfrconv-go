#!/usr/bin/env python3
"""Verify and package Go 0.1.1 native Linux execution evidence."""
from pathlib import Path
import hashlib
import json
import re
import shutil

root = Path(__file__).resolve().parents[2]
parent = root / 'work/go-rss-followup'
work = parent / 'linux'
results = work / 'results'
source = root / 'outputs/jfrconv-go'
digest = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
freeze = json.loads((results / 'source-freeze.json').read_text())
assert digest(parent / 'source-freeze.json') == digest(results / 'source-freeze.json') == '7ac225a55b826584e03b6301b39fbc8be66a094d7fc7be72e581aaccdab98930'
for name, sha in freeze['files'].items():
    assert digest(source / name) == digest(work / 'source' / name) == sha, name
binaries = json.loads((results / 'go-binaries-before.json').read_text())
assert all(digest(work / 'bin' / name) == sha for name, sha in binaries.items())
statuses = json.loads((results / 'exit-status.json').read_text())
assert all(status == 0 for status in statuses.values()), statuses
counts = {}
for package in ('jfr', 'convert', 'cli'):
    expected = {name for path in (work / 'source/internal' / package).glob('*test.go') for name in re.findall(r'^func (Test\w+)\(t \*testing.T\)', path.read_text(), re.M)}
    log = (results / f'{package}-tests.log').read_text()
    rows = re.findall(r'^--- (PASS|FAIL|SKIP): (Test\w+) \(', log, re.M)
    assert {name for _, name in rows} == expected and log.rstrip().endswith('PASS'), (package, rows)
    counts[package] = {value.lower(): sum(status == value for status, _ in rows) for value in ('PASS', 'FAIL', 'SKIP')}
    counts[package]['skipped_tests'] = [name for status, name in rows if status == 'SKIP']
    assert all(name in {'TestStdoutAliasPreservesRegularFilePrefixInBothModes', 'TestNullDeviceIdentityInBothModes'} for name in counts[package]['skipped_tests'])
assert sum(c['pass'] for c in counts.values()) == 63 and sum(c['skip'] for c in counts.values()) == 2 and not any(c['fail'] for c in counts.values()), counts
public = root / 'work/upstream-linux/v41-go'
manifest = json.loads((results / 'public-manifest.json').read_text())
assert all(digest(public / name) == sha for name, sha in manifest['assertion_sources'].items())
public_binaries = json.loads((results / 'public-binaries-before.json').read_text())
assert all(digest(public / name) == sha for name, sha in public_binaries.items())
public_log = (results / 'public/tests.log').read_text()
public_rows = re.findall(r'^(PASS|FAIL|SKIP_[A-Z_]+) \[(\d+)/(\d+)\] (.*?) took ([0-9.,]+) s$', public_log, re.M)
assert len(public_rows) == 1 and public_rows[0][0] == 'PASS', public_log
cgroup = json.loads((results / 'cgroup-report.json').read_text())
assert cgroup['all_checks_passed'] and cgroup['binary_sha256'] == binaries['jfrconv']
summary = {
    'status': 'VERIFIED', 'version': freeze['version'], 'platform': 'Linux arm64',
    'macos_binary_sha256': freeze['native_binary_sha256'], 'linux_binary_sha256': binaries['jfrconv'],
    'source_freeze_sha256': digest(results / 'source-freeze.json'), 'source_files_verified_before_and_after': len(freeze['files']),
    'toolchain': 'Official Go 1.27.4 host crosscompile with CGO_ENABLED=0; CLI and all package tests executed natively on Linux.',
    'security': 'Existing cached Temurin image pinned by local ID. Default Docker capabilities/seccomp; no network, privileged mode, added capabilities, user image changes, compiler or SDK installation. Official Node runtime mounted read-only after TLS download and SHA256 verification. Source, test executables, recordings and public test tree mounted read-only.',
    'node_runtime': json.loads((results / 'node-runtime.json').read_text()),
    'bootstrap_note': 'First disposable attempt stopped before tests because apt could not resolve Ubuntu mirrors. Its environment-only failure log is preserved; successful validation runs offline with a verified read-only Node binary.',
    'tests': counts, 'top_level_tests': 65, 'passed': 63, 'explicit_root_device_probes_skipped': 2,
    'coverage_note': 'Recorded JDK/AP fixtures, wide method IDs and signed location boundaries, frame slab budgets, zero-allocation frame access/event loops, original JavaScript heatmap color oracle, actual 64-input batch, RSS guards and task-owned FIFO/descriptor tests executed. The two root-only system-device probe tests are skipped; they pass in the macOS suite.',
    'public_ldpreload': {'status': 'PASS', 'test': public_rows[0][3], 'assertion_files_unchanged': True, 'assertion_file_count': len(manifest['assertion_sources']), 'revision': manifest['revision'], 'cached_transport_and_native_binaries_unchanged': True, 'native_cli_binary_sha256': binaries['jfrconv']},
    'strict_whole_tree_cgroup': cgroup, 'exit_status': statuses,
}
(results / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
destination = source / 'reports/releases/v0.1.1/linux'
destination.mkdir(parents=True, exist_ok=True)
for path in results.iterdir():
    if path.is_file():
        shutil.copy2(path, destination / path.name)
shutil.copy2(results / 'public/tests.log', destination / 'public-ldpreload.log')
for name in ('linux_prepare.py', 'linux_run.sh', 'linux_launch.py', 'linux_cgroup.py', 'linux_package.py'):
    shutil.copy2(parent / name, destination / name)
print(json.dumps({'tests': counts, 'public_ldpreload': summary['public_ldpreload'], 'cgroup': cgroup, 'destination': str(destination)}, indent=2))
