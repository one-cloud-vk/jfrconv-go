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
semantic = root / 'work/go-v45/final-semantic-jdk21-attempt2'
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
binary = candidate
assert sha(jar) == 'ded43c350f75766b82570ac297c9dd3c3efd69e73de26474615cf2df90e1fc94'
validation = json.loads((semantic / 'validation.json').read_text())
assert not validation['failures'] and len(validation['passed']) == 81
assert validation['go_binary_sha256'] == expected
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
    source = root / 'work/fixtures/ap41.jfr' if name.startswith('ap41-') else root / 'work/fixtures/jdk.jfr' if name.startswith('jdk-') else root / 'outputs/jfrconv-go/tests/fixtures/heatmap-wall45.jfr' if name == 'heatmap-wall' else semantic / 'mixed.jfr'
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
assert sha(candidate) == expected
print('Final22 heatmap comparisons PASS', flush=True)
