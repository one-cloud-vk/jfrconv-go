#!/usr/bin/env python3
"""Go-only checks in the existing image, without seed/compiler capabilities."""
from pathlib import Path
import json
import subprocess

root = Path(__file__).resolve().parents[2]
work = root / 'work/go-rss-followup/linux'
image = json.loads(subprocess.check_output(['docker', 'image', 'inspect', 'eclipse-temurin:21-jdk'], text=True))[0]
image = {key: image.get(key) for key in ('Id', 'RepoDigests', 'Architecture', 'Os', 'Created')}
assert image['Architecture'] == 'arm64' and image['Os'] == 'linux'
(work / 'results/docker-image.json').write_text(json.dumps(image, indent=2) + '\n')
command = ['docker', 'run', '--rm', '--network=none']
for name in ('source', 'bin', 'recordings', 'results'):
    value = f'type=bind,src={work / name},dst=/task/{name}'
    if name != 'results':
        value += ',readonly'
    command += ['--mount', value]
command += ['--mount', f'type=bind,src={work / "node"},dst=/node,readonly',
           '--mount', f'type=bind,src={root / "work/upstream-linux/v41-go"},dst=/public,readonly',
           '--mount', f'type=bind,src={root / "work/go-rss-followup/linux_run.sh"},dst=/task-run.sh,readonly',
           '--entrypoint', '/bin/bash', image['Id'], '/task-run.sh']
(work / 'results/docker-command.json').write_text(json.dumps(command, indent=2) + '\n')
raise SystemExit(subprocess.run(command).returncode)
