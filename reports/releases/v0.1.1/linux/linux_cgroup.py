#!/usr/bin/env python3
"""Run the frozen Go CLI in its own 64 MiB cgroup after compiler cleanup."""
from pathlib import Path
import hashlib
import json
import re
import subprocess

root = Path(__file__).resolve().parents[2]
work = root / 'work/go-rss-followup/linux'
results = work / 'results'
output = work / 'cgroup-output'
output.mkdir(exist_ok=True)
binary = work / 'bin/jfrconv'
fixture = work / 'source/tests/fixtures/mixed.jfr'
manifest = json.loads((results / 'go-binaries-before.json').read_text())
assert hashlib.sha256(binary.read_bytes()).hexdigest() == manifest['jfrconv']
image = json.loads((results / 'docker-image.json').read_text())
container_script = r'''
set -eu
mkdir -p /task-out/tmp /task-out/results
export TMPDIR=/task-out/tmp TMP=/task-out/tmp TEMP=/task-out/tmp
set -- --cpu --memory-stats --memory-limit 16M --max-rss 32M -o collapsed
index=0
while test "$index" -lt 64; do set -- "$@" /input/mixed.jfr; index=$((index+1)); done
/task-bin/jfrconv "$@" /task-out/results
for name in memory.max memory.peak memory.current memory.events memory.swap.max; do
  if test -r "/sys/fs/cgroup/$name"; then
    printf '%s\n' "[$name]"
    cat "/sys/fs/cgroup/$name"
  fi
done > /task-out/kernel-memory.txt
'''
command = ['docker', 'run', '--rm', '--network=none', '--memory=64m', '--memory-swap=64m',
           '--mount', f'type=bind,src={binary},dst=/task-bin/jfrconv,readonly',
           '--mount', f'type=bind,src={fixture},dst=/input/mixed.jfr,readonly',
           '--mount', f'type=bind,src={output},dst=/task-out',
           '--entrypoint', '/bin/bash', image['Id'], '-c', container_script]
(results / 'cgroup-command.json').write_text(json.dumps(command, indent=2) + '\n')
with (results / 'cgroup-run.log').open('w') as log:
    completed = subprocess.run(command, stdout=log, stderr=subprocess.STDOUT)
report = {
    'returncode': completed.returncode,
    'binary_sha256': manifest['jfrconv'],
    'configured_memory_max_bytes': 64 << 20,
    'configured_swap_bytes': 0,
    'inputs': 64,
    'soft_go_memory_limit_bytes': 16 << 20,
    'worker_sampled_rss_limit_bytes': 32 << 20,
    'method': 'Disposable default-security Docker cgroup, --memory=64m --memory-swap=64m, no network or added capabilities. Kernel cgroup memory includes the process tree and charged page cache; it is not a sum of process RSS.',
}
if (output / 'kernel-memory.txt').exists():
    text = (output / 'kernel-memory.txt').read_text()
    sections = re.findall(r'\[([^]]+)\]\n(.*?)(?=\[|\Z)', text, re.S)
    report['kernel_memory'] = {key: value.strip() for key, value in sections}
    (results / 'cgroup-kernel-memory.txt').write_text(text)
(results / 'cgroup-report.json').write_text(json.dumps(report, indent=2) + '\n')
assert completed.returncode == 0, report
kernel = report.get('kernel_memory', {})
if 'memory.max' in kernel:
    assert int(kernel['memory.max']) == 64 << 20, report
if 'memory.peak' in kernel:
    assert int(kernel['memory.peak']) <= 64 << 20, report
if 'memory.events' in kernel:
    events = dict(line.split() for line in kernel['memory.events'].splitlines())
    assert int(events.get('oom', '0')) == 0 and int(events.get('oom_kill', '0')) == 0, report
text = (results / 'cgroup-run.log').read_text()
assert text.count('memory: heap_alloc=') == 64, text
collapsed = (output / 'results/mixed.collapsed').read_text()
assert sum(int(line.rsplit(' ', 1)[1]) for line in collapsed.splitlines()) == 6, collapsed
assert not list((output / 'tmp').iterdir()), 'Worker scratch leaked after successful cgroup run'
assert sorted(path.name for path in (output / 'results').iterdir()) == ['mixed.collapsed']
report['all_checks_passed'] = True
(results / 'cgroup-report.json').write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps(report, indent=2))
