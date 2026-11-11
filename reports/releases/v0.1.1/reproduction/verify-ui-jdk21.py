#!/usr/bin/env python3
"""Reproduce the retained UI corpus for the frozen Go 0.1.1 candidate."""
from pathlib import Path
import argparse
import hashlib
import json
import re
import shutil
import subprocess

root = Path(__file__).resolve().parents[2]
output = root / 'work/go-rss-followup/ui-jdk21'
semantic = root / 'work/go-rss-followup/semantic-jdk21'
candidate = root / 'work/go-rss-followup/candidate/jfrconv'
expected = '785dfc5fd23cf07dae4e26f905d2699ee8c128a63e3256b7b1a255428170d6de'
java = Path('/opt/homebrew/opt/openjdk@21/bin/java')
jar = root / 'work/async-profiler-4.1/build/jar/jfr-converter.jar'
tools = root / 'outputs/jfrconv-go/tools'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(command):
    return subprocess.run(list(map(str, command)), check=True, capture_output=True,
                          text=True, timeout=120)


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--resume-heatmaps', action='store_true')
args = parser.parse_args()
assert digest(candidate) == expected
assert digest(jar) == '2464b34abd47243941d81d53110591d66d7db3b3fae46c79fc85057eeddda19b'
frozen = output / 'jfrconv-measured'
if args.resume_heatmaps:
    assert digest(frozen) == expected
    manifest = json.loads((output / 'manifest.json').read_text())
    assert manifest['go_binary_sha256'] == expected
else:
    assert not output.exists(), 'Use a new output directory; preserve every previous run'
    output.mkdir(parents=True)
    frozen = output / 'jfrconv-measured'
    shutil.copyfile(candidate, frozen)
    frozen.chmod(0o555)
    original = json.loads((root / 'work/go-ui-final/manifest.json').read_text())
    manifest = {
        'implementation': 'native Go', 'version': '0.1.1',
        'go_binary_sha256': expected, 'binary_source': str(candidate),
        'java_jar_sha256': digest(jar), 'java_executable': str(java),
        'java_version': run([java, '-version']).stderr.strip(),
        'prior_corpus': str(root / 'work/go-ui-final/manifest.json'),
        'cases': [],
    }
    for case in original['cases']:
        directory = output / case['case']
        directory.mkdir()
        source = Path(case['source'])
        assert digest(source) == case['source_sha256'], source
        files, commands = {}, {}
        for name, command in [('java', [java, '-jar', jar]), ('go', [frozen])]:
            destination = directory / (name + '.html')
            command += [*case['flags'], '-o', case['format'], source, destination]
            run(command)
            files[name] = str(destination.relative_to(output))
            commands[name] = list(map(str, command))
        manifest['cases'].append({
            'case': case['case'], 'format': case['format'], 'flags': case['flags'],
            'source': str(source), 'source_sha256': digest(source),
            'files': files, 'commands': commands,
        })
        print('generated', case['case'], flush=True)
    (output / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')

    shells = {
        'native_binary_sha256': expected,
        'method': 'Remove complete script elements and compare remaining HTML/CSS bytes; original JavaScript is checked by the renderer below.',
        'cases': [],
    }
    for case in manifest['cases']:
        if case['format'] == 'heatmap':
            continue
        hashes = {}
        for name, path in case['files'].items():
            shell = re.sub(r'<script\b[^>]*>.*?</script>', '',
                           (output / path).read_text(), flags=re.DOTALL)
            hashes[name] = hashlib.sha256(shell.encode()).hexdigest()
        same = hashes['java'] == hashes['go']
        shells['cases'].append({'case': case['case'], 'same_HTML_CSS_shell': same, 'sha256': hashes})
        assert same, case['case']
    (output / 'static-shell.json').write_text(json.dumps(shells, indent=2) + '\n')

    for dpr in [1, 2]:
        result = run(['node', tools / 'ui_renderer_parity.js', '--corpus', output / 'manifest.json',
                      '--output', output / f'renderer-dpr{dpr}', '--dpr', str(dpr), '--strict'])
        (output / f'renderer-dpr{dpr}.log').write_text(result.stdout + result.stderr)
        print('renderer DPR', dpr, 'PASS', flush=True)

jdk_pair = output / 'jdk-heatmap-cpu'
jdk_pair.mkdir(exist_ok=True)
for name, command in [('java', [java, '-jar', jar]), ('go', [frozen])]:
    destination = jdk_pair / (name + '.html')
    assert not destination.exists(), 'Preserve every generated JDK CPU page'
    run([*command, '--cpu', '-o', 'heatmap', root / 'work/fixtures/jdk.jfr', destination])

colors = {'native_binary_sha256': expected, 'cases': []}
pairs = [(path.name.removesuffix('.java.heatmap.html'), path,
          path.with_name(path.name.replace('.java.', '.go.')))
         for path in sorted(semantic.glob('*.java.heatmap.html'))]
pairs += [('ap41-heatmap-cpu', output / 'ap-heatmap/java.html', output / 'ap-heatmap/go.html'),
          ('jdk-heatmap-cpu', output / 'jdk-heatmap-cpu/java.html', output / 'jdk-heatmap-cpu/go.html')]
for name, java_file, go_file in pairs:
    color = json.loads(run(['node', tools / 'heatmap_colors.js', java_file, go_file]).stdout)
    decoded = [json.loads(run(['node', tools / 'heatmap_decode.js', path]).stdout)
               for path in [java_file, go_file]]
    assert color['same_used_method_IDs_RGB'] and decoded[0] == decoded[1], name
    colors['cases'].append({
        'case': name, 'flags': ['--' + name.rsplit('-', 1)[1]],
        'java_file': str(java_file), 'native_file': str(go_file),
        **color, 'same_stack_timeline_counts': True, 'semantic_timeline': decoded[0],
    })
    print('heatmap', name, 'PASS', flush=True)
assert len(colors['cases']) == 10
colors['all_exact'] = True
colors['nonempty_cases'] = sum(case['java_methods'] != 0 for case in colors['cases'])
colors['empty_cases'] = len(colors['cases']) - colors['nonempty_cases']
(output / 'heatmap-colors.json').write_text(json.dumps(colors, indent=2) + '\n')
assert digest(candidate) == expected and digest(frozen) == expected
print('UI and exact heatmap checks complete', flush=True)
