#!/usr/bin/env python3
"""Crosscompile only the final frozen Go4.5 candidate; no Linux SDK required."""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess

root = Path(__file__).resolve().parents[3]
work = root / 'work/go-v45/linux'
freeze_file = root / 'work/go-v45/source-freeze.json'
digest = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
assert digest(freeze_file) == '73968c9525ecf8895d3344f29fed525aa4445d1a23487bc6ff6920c0ff88f5d8'
freeze = json.loads(freeze_file.read_text())
assert freeze['version'] == '0.2.0' and digest(Path(freeze['native_binary'])) == freeze['native_binary_sha256']
source = root / 'outputs/jfrconv-go'
snapshot, bins = work / 'go-source', work / 'bin'
snapshot.mkdir()
bins.mkdir()
for name, sha in freeze['files'].items():
    assert digest(source / name) == sha, name
    target = snapshot / name
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source / name, target)
    assert digest(target) == sha
shutil.copy2(freeze_file, work / 'results/source-freeze.json')
recordings = work / 'recordings'
recordings.mkdir()
for name in ('jdk.jfr', 'ap41.jfr'):
    shutil.copy2(root / 'work/fixtures' / name, recordings / name)
(work / 'results/recorded-inputs.json').write_text(json.dumps({p.name: digest(p) for p in recordings.iterdir()}, indent=2) + '\n')
go = ['sh', str(root / 'work/go.sh')]
environment = dict(os.environ, GOOS='linux', GOARCH='arm64', GOENV='off', CGO_ENABLED='0', GOTMPDIR=str(root / 'work/go-tmp'))
subprocess.run(go + ['build', '-trimpath', '-ldflags=-s -w', '-o', str(bins / 'jfrconv'), './cmd/jfrconv'], cwd=snapshot, env=environment, check=True)
for package in ('jfr', 'convert', 'cli'):
    subprocess.run(go + ['test', '-c', '-trimpath', '-o', str(bins / (package + '.test')), './internal/' + package], cwd=snapshot, env=environment, check=True)
with (work / 'results/go-build-info.log').open('w') as log:
    subprocess.run(go + ['version', '-m', str(bins / 'jfrconv')], cwd=snapshot, env=environment, stdout=log, check=True)
(work / 'results/go-binaries-before.json').write_text(json.dumps({p.name: digest(p) for p in bins.iterdir()}, indent=2) + '\n')
print(json.dumps({'version': freeze['version'], 'source_files': len(freeze['files']), 'linux_binary_sha256': digest(bins / 'jfrconv')}, indent=2))
