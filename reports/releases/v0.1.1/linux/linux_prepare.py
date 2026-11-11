#!/usr/bin/env python3
"""Freeze Go 0.1.1 and crosscompile native Linux executables."""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess

root = Path(__file__).resolve().parents[2]
parent = root / 'work/go-rss-followup'
work = parent / 'linux'
source = root / 'outputs/jfrconv-go'
freeze_file = parent / 'source-freeze.json'
digest = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
assert digest(freeze_file) == '7ac225a55b826584e03b6301b39fbc8be66a094d7fc7be72e581aaccdab98930'
freeze = json.loads(freeze_file.read_text())
assert freeze['native_binary_sha256'] == digest(Path(freeze['native_binary']))
snapshot = work / 'source'
if snapshot.exists():
    raise SystemExit(f'Refusing to replace snapshot: {snapshot}')
snapshot.mkdir(parents=True)
results = work / 'results'
results.mkdir()
bins = work / 'bin'
bins.mkdir()
for name, sha in freeze['files'].items():
    assert digest(source / name) == sha, name
    target = snapshot / name
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source / name, target)
    assert digest(target) == sha
shutil.copy2(freeze_file, results / 'source-freeze.json')
recordings = work / 'recordings'
recordings.mkdir()
for name in ('jdk.jfr', 'ap41.jfr'):
    shutil.copy2(root / 'work/fixtures' / name, recordings / name)
(results / 'recorded-inputs.json').write_text(json.dumps({p.name: digest(p) for p in recordings.iterdir()}, indent=2) + '\n')
go = ['sh', str(root / 'work/go.sh')]
environment = dict(os.environ, GOOS='linux', GOARCH='arm64', GOENV='off', CGO_ENABLED='0', GOTMPDIR=str(root / 'work/go-tmp'))
subprocess.run(go + ['build', '-trimpath', '-ldflags=-s -w', '-o', str(bins / 'jfrconv'), './cmd/jfrconv'], cwd=snapshot, env=environment, check=True)
for package in ('jfr', 'convert', 'cli'):
    subprocess.run(go + ['test', '-c', '-trimpath', '-o', str(bins / (package + '.test')), './internal/' + package], cwd=snapshot, env=environment, check=True)
with (results / 'go-build-info.log').open('w') as log:
    subprocess.run(go + ['version', '-m', str(bins / 'jfrconv')], cwd=snapshot, env=environment, stdout=log, check=True)
(results / 'go-binaries-before.json').write_text(json.dumps({p.name: digest(p) for p in bins.iterdir()}, indent=2) + '\n')
public = root / 'work/upstream-linux/v41-go'
manifest = json.loads((public / 'public-test-manifest.json').read_text())
assert all(digest(public / name) == sha for name, sha in manifest['assertion_sources'].items())
assert digest(public / 'src/converter/one/convert/GoBridge.java') == digest(source / 'tools/upstream/GoBridge.java')
shutil.copy2(public / 'public-test-manifest.json', results / 'public-manifest.json')
(results / 'public-binaries-before.json').write_text(json.dumps({name: digest(public / name) for name in ('build/test.jar', 'build/jar/jfr-converter.jar', 'build/lib/libasyncProfiler.so')}, indent=2) + '\n')
print(json.dumps({'freeze_sha256': digest(freeze_file), 'source_files': len(freeze['files']), 'linux_binary_sha256': digest(bins / 'jfrconv')}, indent=2))
