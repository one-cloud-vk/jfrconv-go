#!/usr/bin/env python3
"""Package compact Linux4.5 evidence after all task-owned containers exit."""
from pathlib import Path
import hashlib
import json
import re
import shutil

root = Path(__file__).resolve().parents[3]
work = root / 'work/go-v45/linux'
results = work / 'results'
project = root / 'outputs/jfrconv-go'
digest = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
freeze = json.loads((results / 'source-freeze.json').read_text())
freeze_sha = '73968c9525ecf8895d3344f29fed525aa4445d1a23487bc6ff6920c0ff88f5d8'
assert digest(results / 'source-freeze.json') == digest(root / 'work/go-v45/build-source-freeze.json') == freeze_sha
final_freeze_sha = '680a235d0231be4c6c001aea6b633ece193b19893f7700f89128e7b627147897'
assert digest(root / 'work/go-v45/source-freeze.json') == final_freeze_sha
shutil.copy2(root / 'work/go-v45/build-source-freeze.json', results / 'build-source-freeze.json')
shutil.copy2(root / 'work/go-v45/source-freeze.json', results / 'final-source-freeze.json')
shutil.copy2(root / 'work/go-v45/helper-amendment.json', results / 'helper-amendment.json')
changes = []
for name, sha in freeze['files'].items():
    assert digest(work / 'go-source' / name) == sha, name
    actual = digest(project / name)
    if actual != sha:
        changes.append({'file': name, 'linux_snapshot_sha256': sha, 'current_sha256': actual})
assert all(item['file'] in {'tools/validate.py', 'tools/ui_renderer_parity.js'} for item in changes), changes
binaries = json.loads((results / 'go-binaries-before.json').read_text())
assert all(digest(work / 'bin' / name) == sha for name, sha in binaries.items())
statuses = json.loads((results / 'exit-status.json').read_text())
assert all(code == 0 for code in statuses.values()), statuses
counts = {}
for package in ('jfr', 'convert', 'cli'):
    expected = {name for path in (work / 'go-source/internal' / package).glob('*test.go') for name in re.findall(r'^func (Test\w+)\(t \*testing.T\)', path.read_text(), re.M)}
    log = (results / (package + '-tests.log')).read_text()
    rows = re.findall(r'^--- (PASS|FAIL|SKIP): (Test\w+) \(', log, re.M)
    assert {name for _, name in rows} == expected and log.rstrip().endswith('PASS'), package
    counts[package] = {key.lower(): sum(status == key for status, _ in rows) for key in ('PASS', 'FAIL', 'SKIP')}
    counts[package]['skipped_tests'] = [name for status, name in rows if status == 'SKIP']
    assert all(name in {'TestStdoutAliasPreservesRegularFilePrefixInBothModes', 'TestNullDeviceIdentityInBothModes'} for name in counts[package]['skipped_tests'])
assert sum(item['pass'] for item in counts.values()) == 84 and sum(item['skip'] for item in counts.values()) == 2
assert 'Ran 6 tests' in (results / 'python-tests.log').read_text() and (results / 'python-tests.log').read_text().rstrip().endswith('OK')
source_hashes = json.loads((results / 'public-source-before.json').read_text())
assert all(digest(work / 'public-native' / name) == sha for name, sha in source_hashes.items())
manifest = json.loads((work / 'public-native/public-test-manifest.json').read_text())
assert manifest['commit'] == '11aaea310cb75843f0e7532365d8cedc8d0e14c8'
assert all(digest(work / 'public-native' / name) == sha for name, sha in manifest['assertion_sources'].items())
public = json.loads((results / 'public-native/public-tests.json').read_text())
assert public['counts'] == {'PASS': 27} and public['effective_returncode'] == public['returncode'] == 0
assert public['native_binary_sha256'] == binaries['jfrconv']
assert public['latency_native_differential']['passed'] and public['latency_native_differential']['same_full_stack_count_map']
assert len((results / 'public-native/scope.tsv').read_text().splitlines()) == 27
span = json.loads((results / 'actual-span-api/report.json').read_text())
assert span['status'] == 'PASS' and span['passed'] == 10 and all(item['same_full_stack_count_map'] for item in span['cases'])
assert span['native_binary_sha256'] == binaries['jfrconv']
cgroup = json.loads((results / 'cgroup-report.json').read_text())
assert cgroup['all_checks_passed'] and cgroup['binary_sha256'] == binaries['jfrconv']
node = json.loads((root / 'work/go-rss-followup/linux/results/node-runtime.json').read_text())
(results / 'node-runtime.json').write_text(json.dumps(node, indent=2) + '\n')
summary = {
    'status': 'VERIFIED', 'version': '0.2.0', 'compatibility_target': 'async-profiler4.5',
    'upstream_commit': manifest['commit'], 'platform': 'Linux arm64',
    'native_macos_binary_sha256': freeze['native_binary_sha256'], 'native_linux_binary_sha256': binaries['jfrconv'],
    'source_freeze_sha256': freeze_sha, 'source_files_verified_before_and_after': len(freeze['files']),
    'final_source_freeze_sha256': final_freeze_sha,
    'build_source_freeze': 'build-source-freeze.json', 'final_source_freeze': 'final-source-freeze.json',
    'helper_amendment': 'helper-amendment.json',
    'current_native_and_test_source_matches_snapshot': True,
    'post_validation_helper_amendments': changes,
    'helper_execution_note': 'Linux executed the immutable73968c95 snapshot. Current UI renderer/validation helper amendments listed here were not executed on Linux; their fresh checks belong to the Mac semantic/UI reports. Native/test source and candidate binary are unchanged.',
    'security': 'Existing cached Temurin image pinned by local ID; Docker default capabilities/seccomp. No privileged mode, added capabilities, host sysctls/kernel/cgroup changes, image pulls/commits, or global SDK installation. gcc/g++/make/Python installed only in the removed disposable build/test container, using signed Ubuntu package indexes and APT::Sandbox::User=root. Official checksum-verified Node22.22.1 reused read-only. Cgroup proof used a separate offline --rm container.',
    'environment_exceptions': [], 'kernel_perf_event_paranoid_observed': 2,
    'prerequisite_attempt_history': 'The first setup copied the src/test/docs manifest, which omits root Makefile, and stopped before compilation/tests with no build-test-java target. The second setup added the Makefile from the pinned extracted source, recorded its hash separately, and completed all builds/tests. Initial setup logs are retained as attempt1-*; no assertion was changed.',
    'go_tests': counts, 'top_level_go_tests': 86, 'passed_go_tests': 84, 'root_device_probes_skipped': 2,
    'python_tests_passed': 6, 'public_tests': {'declared': 27, 'passed': 27, 'failed': 0, 'skipped': 0, 'assertion_files_unchanged': True, 'assertion_file_count': len(manifest['assertion_sources']), 'scope': 'public-scope.tsv', 'report': 'public-tests.json'},
    'native_public_latency_supplement': public['latency_native_differential'],
    'actual_span_api_proof': {'passed': 10, 'report': 'actual-span-api.json', 'source': span['source']},
    'strict_whole_tree_cgroup': cgroup, 'node_runtime': node, 'exit_status': statuses,
    'omitted_artifacts': 'Compiled executables, SDKs, Node archive/runtime, generated HTML, profiles and converted payloads remain only in task work directories. This package contains compact logs, hashes, source manifests and executed scripts.'
}
(results / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
destination = project / 'reports/releases/v0.2.0/linux'
destination.mkdir(parents=True, exist_ok=True)
for path in results.iterdir():
    if path.is_file() and path.suffix != '.cid':
        shutil.copy2(path, destination / path.name)
for filename, target in [('public-native/public-tests.json', 'public-tests.json'), ('public-native/scope.tsv', 'public-scope.tsv'), ('public-native/tests.log', 'public-tests.log'), ('actual-span-api/report.json', 'actual-span-api.json')]:
    shutil.copy2(results / filename, destination / target)
for path in (results / 'actual-span-api').glob('*-jdk-events.tsv'):
    shutil.copy2(path, destination / path.name)
shutil.copy2(work / 'public-native/public-test-manifest.json', destination / 'public-transport-manifest.json')
oracle = destination / 'latency-native-oracle'
oracle.mkdir(exist_ok=True)
for path in (results / 'public-native/latency-native-oracle').iterdir():
    if path.suffix in {'.json', '.log'}:
        shutil.copy2(path, oracle / path.name)
for name in ('build.sh', 'build_launch.py', 'prepare_go.py', 'run-final.sh', 'SpanInspect.java', 'span_proof.py', 'extra_launch.py', 'cgroup.py', 'package.py', 'PublicScope.java', 'run_public45.py', 'latency_native_oracle.py'):
    shutil.copy2(work / name, destination / name)
print(json.dumps({'destination': str(destination), 'go': counts, 'python': 6, 'public': 27, 'actual_span_cases': 10, 'cgroup_peak': cgroup['kernel_memory'].get('memory.peak'), 'helper_changes': changes, 'files': sum(path.is_file() for path in destination.rglob('*')), 'bytes': sum(path.stat().st_size for path in destination.rglob('*') if path.is_file())}, indent=2))
