#!/usr/bin/env python3
from pathlib import Path
import json
import subprocess

root = Path(__file__).resolve().parents[3]
work = root / 'work/go-v45/linux'
image = json.loads(subprocess.check_output(['docker', 'image', 'inspect', 'eclipse-temurin:21-jdk'], text=True))[0]
image = {key: image.get(key) for key in ('Id', 'RepoDigests', 'Architecture', 'Os', 'Created')}
assert image['Os'] == 'linux' and image['Architecture'] == 'arm64'
(work / 'results/docker-image.json').write_text(json.dumps(image, indent=2) + '\n')
command = ['docker', 'run', '--rm', '--cidfile', str(work / 'results/build-container.cid'),
           '--mount', f'type=bind,src={work},dst=/task',
           '--mount', f'type=bind,src={root / "work/go-rss-followup/linux/node"},dst=/node,readonly',
           '--entrypoint', '/bin/bash', image['Id'], '/task/build.sh']
(work / 'results/build-docker-command.json').write_text(json.dumps(command, indent=2) + '\n')
raise SystemExit(subprocess.run(command).returncode)
