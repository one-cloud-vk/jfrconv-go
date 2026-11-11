#!/usr/bin/env python3
"""Disposable Linux validation with the established default-security bootstrap."""
from pathlib import Path
import json
import subprocess

root = Path(__file__).resolve().parents[2]
work = root / 'work/upstream-linux'
results = work / 'final-native-results'
results.mkdir(exist_ok=True)
metadata = json.loads(subprocess.check_output(['docker', 'image', 'inspect', 'eclipse-temurin:21-jdk'], text=True))[0]
image = {key: metadata.get(key) for key in ('Id', 'RepoDigests', 'Architecture', 'Os', 'Created')}
assert image['Os'] == 'linux' and image['Architecture'] == 'arm64', image
(results / 'docker-image.json').write_text(json.dumps(image, indent=2) + '\n')
mounts = [
    (root / 'work/linux-env/usr', '/seed', True),
    (root / 'work/linux-env/etc', '/seed-etc', True),
    (root / 'work/linux-env/var', '/seed-var', True),
    (work, '/work', False),
    (Path('/Users/v.matskevich/.cargo/registry'), '/work/cargo/registry', True),
]
command = ['docker', 'run', '--rm']
for source, target, readonly in mounts:
    value = f'type=bind,src={source},dst={target}'
    if readonly:
        value += ',readonly'
    command += ['--mount', value]
for target, size in [('/usr', 1610612736), ('/var', 536870912), ('/etc', 67108864), ('/tmp', 536870912)]:
    command += ['--tmpfs', f'{target}:rw,exec,nosuid,nodev,size={size}']
loader = '/seed/lib/aarch64-linux-gnu/ld-linux-aarch64.so.1'
bootstrap = f'''{loader} --argv0 cp --library-path /seed/lib/aarch64-linux-gnu /seed/bin/gnucp -a --no-preserve=ownership,mode,xattr /seed/. /usr/ &&
{loader} --argv0 chmod --library-path /seed/lib/aarch64-linux-gnu /seed/bin/gnuchmod -R u+rwx /usr &&
/usr/bin/gnuchmod -R a+rX /usr &&
for file in resolv.conf hosts hostname; do /usr/bin/gnucp /etc/$file /tmp/jfrconv-$file; done &&
/usr/bin/gnucp -a --no-preserve=ownership,mode,xattr /seed-etc/. /etc/ &&
for file in resolv.conf hosts hostname; do /usr/bin/gnucp /tmp/jfrconv-$file /etc/$file; done &&
/usr/bin/gnucp -a --no-preserve=ownership,mode,xattr /seed-var/. /var/ &&
/usr/bin/gnuchmod -R u+rwx /etc /var &&
/usr/bin/gnuchmod -R a+rX /etc /var &&
exec /usr/bin/sh /work/final-native-run.sh'''
command += ['--entrypoint', loader, image['Id'], '--argv0', 'sh', '--library-path', '/seed/lib/aarch64-linux-gnu', '/seed/bin/sh', '-c', bootstrap]
(results / 'docker-command.json').write_text(json.dumps(command, indent=2) + '\n')
raise SystemExit(subprocess.run(command).returncode)
