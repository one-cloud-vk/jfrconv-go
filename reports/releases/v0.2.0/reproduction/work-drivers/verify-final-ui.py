#!/usr/bin/env python3
"""Validate frozen Go 0.2.0 pages against the pinned Java 4.5 renderer/data."""
from pathlib import Path
import hashlib
import json
import re
import shutil
import subprocess

root = Path(__file__).resolve().parents[2]
directory = root / 'work/go-v45/final-ui-jdk21'
semantic = root / 'work/go-v45/final-semantic-jdk21'
candidate = root / 'work/go-v45/candidate/jfrconv'
expected = 'a9d7e8fc1fb440157df5f56ae34029e78cdf5b79005bc38d7a9406da4df1feb5'
java = Path('/opt/homebrew/opt/openjdk@21/bin/java')
jar = root / 'work/async-profiler-4.5/build/jar/jfr-converter.jar'
tools = root / 'outputs/jfrconv-go/tools'


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(command):
    result = subprocess.run(list(map(str, command)), capture_output=True, text=True, timeout=120)
    if result.returncode:
        raise RuntimeError(f'{list(map(str, command))}\n{result.stdout}\n{result.stderr}')
    return result


assert sha(candidate) == expected
assert sha(jar) == 'ded43c350f75766b82570ac297c9dd3c3efd69e73de26474615cf2df90e1fc94'
freeze_file = root / 'work/go-v45/source-freeze.json'
assert sha(freeze_file) == '73968c9525ecf8895d3344f29fed525aa4445d1a23487bc6ff6920c0ff88f5d8'
freeze = json.loads(freeze_file.read_text())
assert all(sha(root / 'outputs/jfrconv-go' / name) == value for name, value in freeze['files'].items())
assert not directory.exists(), 'Preserve previous attempts; choose a new directory'
directory.mkdir(parents=True)
binary = directory / 'jfrconv-measured'
shutil.copyfile(candidate, binary)
binary.chmod(0o555)
manifest = {
    'implementation': 'native Go', 'version': '0.2.0', 'compatibility_target': 'async-profiler v4.5',
    'upstream_commit': freeze['upstream_commit'], 'go_binary_sha256': expected,
    'java_jar_sha256': sha(jar), 'java': str(java),
    'java_version': run([java, '-version']).stderr.strip(),
    'source_freeze_sha256': sha(freeze_file), 'cases': [],
}
old = json.loads((root / 'work/go-ui-final/manifest.json').read_text())
for case in old['cases']:
    source = Path(case['source'])
    assert sha(source) == case['source_sha256'], source
    case_directory = directory / case['case']
    case_directory.mkdir()
    files, commands = {}, {}
    for kind, command in [('java', [java, '-jar', jar]), ('go', [binary])]:
        output = case_directory / (kind + '.html')
        command += [*case['flags'], '-o', case['format'], source, output]
        run(command)
        files[kind] = str(output.relative_to(directory))
        commands[kind] = list(map(str, command))
    manifest['cases'].append({
        'case': case['case'], 'format': case['format'], 'flags': case['flags'],
        'source': str(source), 'source_sha256': sha(source), 'files': files, 'commands': commands,
    })
    print('generated', case['case'], flush=True)
(directory / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
shells = {'go_binary_sha256': expected,
          'method': 'Remove complete script elements, compare remaining HTML/CSS bytes; original JavaScript is separately executed by the renderer.',
          'cases': []}
for case in manifest['cases']:
    if case['format'] == 'heatmap':
        continue
    digests = {}
    for kind, path in case['files'].items():
        shell = re.sub(r'<script\b[^>]*>.*?</script>', '', (directory / path).read_text(), flags=re.DOTALL)
        digests[kind] = hashlib.sha256(shell.encode()).hexdigest()
    same = digests['java'] == digests['go']
    shells['cases'].append({'case': case['case'], 'same_HTML_CSS_shell': same, 'sha256': digests})
    assert same, case['case']
(directory / 'static-shell.json').write_text(json.dumps(shells, indent=2) + '\n')
for dpr in [1, 2]:
    result = run(['node', tools / 'ui_renderer_parity.js', '--corpus', directory / 'manifest.json',
                  '--output', directory / f'renderer-dpr{dpr}', '--dpr', dpr, '--strict'])
    (directory / f'renderer-dpr{dpr}.log').write_text(result.stdout + result.stderr)
    print('renderer DPR', dpr, 'PASS', flush=True)


def heatmap_proof(case, java_file, go_file, flags, source):
    color = json.loads(run(['node', tools / 'heatmap_colors.js', java_file, go_file]).stdout)
    decoded = [json.loads(run(['node', tools / 'heatmap_decode.js', path]).stdout)
               for path in [java_file, go_file]]
    assert color['same_used_method_IDs_RGB'] and decoded[0] == decoded[1], case
    print('heatmap', case, 'PASS', flush=True)
    return {'case': case, 'flags': flags, 'source': str(source), 'source_sha256': sha(source),
            'files': {'java': str(java_file), 'go': str(go_file)}, **color,
            'timeline_equal': True, 'semantic_timeline': decoded[0]}


def generate_heatmap(case, source, flags):
    target = directory / 'heatmap-extra' / case
    target.mkdir(parents=True)
    files = {}
    for kind, command in [('java', [java, '-jar', jar]), ('go', [binary])]:
        output = target / (kind + '.html')
        run([*command, *flags, '-o', 'heatmap', source, output])
        files[kind] = output
    return heatmap_proof(case, files['java'], files['go'], flags, source)


base = []
for name in ['ap41-heatmap-alloc', 'ap41-heatmap-lock', 'ap41-heatmap-wall',
             'heatmap-cpu', 'heatmap-wall', 'jdk-heatmap-alloc', 'jdk-heatmap-lock', 'jdk-heatmap-wall']:
    source = root / 'work/fixtures/ap41.jfr' if name.startswith('ap41-') else root / 'work/fixtures/jdk.jfr' if name.startswith('jdk-') else semantic / 'mixed.jfr'
    flags = ['--' + name.rsplit('-', 1)[1]]
    base.append(heatmap_proof(name, semantic / (name + '.java.heatmap.html'),
                              semantic / (name + '.go.heatmap.html'), flags, source))
base.append(heatmap_proof('ap41-heatmap-cpu', directory / 'ap-heatmap/java.html',
                          directory / 'ap-heatmap/go.html', ['--cpu'], root / 'work/fixtures/ap41.jfr'))
base.append(generate_heatmap('jdk-heatmap-cpu', root / 'work/fixtures/jdk.jfr', ['--cpu']))
probes = []
for case in json.loads((root / 'work/go-v45/heatmap-probe/report.json').read_text()):
    name = case['case']
    source = root / ('outputs/jfrconv-go/tests/fixtures/heatmap-order.jfr' if name == 'order' else
                     'outputs/jfrconv-go/tests/fixtures/heatmap-start.jfr' if name == 'start' else
                     'work/go-v45/fixtures/span-api45.jfr' if name == 'span-request' else
                     'outputs/jfrconv-go/tests/fixtures/mixed.jfr')
    probes.append(generate_heatmap('probe-' + name, source, case['flags']))
wall = []
wall_source = root / 'outputs/jfrconv-go/tests/fixtures/heatmap-wall45.jfr'
for name, flags in [('wall', ['--wall']), ('threaded', ['--wall', '--threads']),
                    ('filter', ['--wall', '--include', 'demo/Worker.*'])]:
    wall.append(generate_heatmap('wall45-' + name, wall_source, flags))
report = {'version': '0.2.0', 'go_binary_sha256': expected, 'java_jar_sha256': sha(jar),
          'all_exact': True, 'base_modes': base, 'reference_order_thread_probes': probes,
          'weighted_wall_first_time_probes': wall, 'exact_comparisons': len(base) + len(probes) + len(wall)}
assert len(base) == 10 and len(probes) == 9 and len(wall) == 3
(directory / 'heatmap-colors.json').write_text(json.dumps(report, indent=2) + '\n')
assert sha(candidate) == expected and sha(binary) == expected
assert all(sha(root / 'outputs/jfrconv-go' / name) == value for name, value in freeze['files'].items())
print('Final4.5 UI/ID/RGB validation complete', flush=True)
