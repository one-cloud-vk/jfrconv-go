#!/usr/bin/env python3
"""Freeze task-owned source copies and crosscompile Go tests after root GO."""
from pathlib import Path
import hashlib
import json
import os
import shutil
import subprocess

root = Path(__file__).resolve().parents[2]
work = root / 'work/upstream-linux'
results = work / 'final-native-results'
results.mkdir(exist_ok=True)

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def snapshot(name, parts):
    source = root / ('outputs/jfrconv-' + name)
    target = work / ('final-' + ('rust' if name == 'rs' else 'go') + '-source')
    if target.exists():
        raise SystemExit(f'Refusing to replace frozen snapshot: {target}')
    target.mkdir()
    for part in parts:
        path = source / part
        if path.is_dir():
            shutil.copytree(path, target / part, ignore=shutil.ignore_patterns('__pycache__', '*.pyc'))
        elif path.is_file():
            shutil.copy2(path, target / part)
    hashes = {str(p.relative_to(target)): digest(p) for p in sorted(target.rglob('*')) if p.is_file()}
    assert all(digest(source / p) == sha for p, sha in hashes.items())
    return {'project': str(source), 'snapshot': str(target), 'files': hashes,
            'tree_sha256': hashlib.sha256(json.dumps(hashes, sort_keys=True, separators=(',', ':')).encode()).hexdigest()}

sources = {
    'rust': snapshot('rs', ['Cargo.toml', 'Cargo.lock', 'src', 'assets', 'tests', 'tools']),
    'go': snapshot('go', ['go.mod', 'cmd', 'internal', 'assets', 'tests', 'tools']),
}
recordings = work / 'final-real-inputs'
recordings.mkdir(exist_ok=True)
for name in ('jdk.jfr', 'ap41.jfr'):
    shutil.copy2(root / 'work/fixtures' / name, recordings / name)
(results / 'recorded-inputs.json').write_text(json.dumps({p.name: digest(p) for p in sorted(recordings.iterdir())}, indent=2) + '\n')
(results / 'source-before.json').write_text(json.dumps(sources, indent=2) + '\n')
go_source = work / 'final-go-source'
bins = work / 'final-go-bin'
bins.mkdir(exist_ok=True)
go = ['sh', str(root / 'work/go.sh')]
environment = dict(os.environ, GOOS='linux', GOARCH='arm64', GOENV='off', CGO_ENABLED='0', GOTMPDIR=str(root / 'work/go-tmp'))
subprocess.run(go + ['build', '-trimpath', '-ldflags=-s -w', '-o', str(bins / 'jfrconv'), './cmd/jfrconv'], cwd=go_source, env=environment, check=True)
for package in ('jfr', 'convert', 'cli'):
    subprocess.run(go + ['test', '-c', '-trimpath', '-o', str(bins / (package + '.test')), './internal/' + package], cwd=go_source, env=environment, check=True)
with (results / 'go-build-info.log').open('w') as output:
    subprocess.run(go + ['version', '-m', str(bins / 'jfrconv')], cwd=go_source, env=environment, stdout=output, check=True)
(results / 'go-binaries-before.json').write_text(json.dumps({p.name: digest(p) for p in sorted(bins.iterdir()) if p.is_file()}, indent=2) + '\n')
stage = work / 'v41-go'
if stage.exists():
    raise SystemExit(f'Refusing to replace public Go test tree: {stage}')
subprocess.run(['python3', str(go_source / 'tools/upstream_tests.py'), 'stage', '--source', str(root / 'work/async-profiler-4.1'), '--ref', 'v4.1', '--dest', str(stage), '--suite', 'v4.1', '--bridge'], check=True)
# Public test assertions are recompiled from the staged sources. Reuse only the
# native profiler/test library cache, generated OTLP dependency and API JAR;
# make will rebuild either native products or Java products when needed.
for part in ('build', 'test/gen', 'test/deps'):
    cached = work / 'v41-java' / part
    if cached.exists():
        shutil.copytree(cached, stage / part, dirs_exist_ok=True)
for product in ('build/test.jar', 'build/jar/jfr-converter.jar', 'build/bin/jfrconv'):
    path = stage / product
    if path.exists():
        path.unlink()
print(json.dumps({'prepared': True, 'sources': {name: value['tree_sha256'] for name, value in sources.items()}, 'linux_go_binaries': str(bins)}, indent=2))
