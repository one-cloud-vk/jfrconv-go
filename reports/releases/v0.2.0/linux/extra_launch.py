#!/usr/bin/env python3
from pathlib import Path
import subprocess

work = Path(__file__).resolve().parent
container = (work / 'results/build-container.cid').read_text().strip()
assert len(container) == 64 and all(ch in '0123456789abcdef' for ch in container)
command = ['docker', 'exec', container, '/bin/bash', '-c', 'javac -d /task/extra-classes /task/SpanInspect.java && python3 /task/span_proof.py']
raise SystemExit(subprocess.run(command).returncode)
