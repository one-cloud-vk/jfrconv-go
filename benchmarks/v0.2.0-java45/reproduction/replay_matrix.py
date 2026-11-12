#!/usr/bin/env python3
"""Portable replay adapter; distinct from the original measured runner snapshot."""
from pathlib import Path
import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
from generate_inputs import NAMES, digest

BASE = Path(__file__).resolve().parent


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--native', required=True, type=Path)
    p.add_argument('--java', required=True)
    p.add_argument('--java-jar', required=True, type=Path)
    p.add_argument('--go-tool', default='go')
    p.add_argument('--inputs', required=True, type=Path)
    p.add_argument('--output', required=True, type=Path)
    p.add_argument('--case', action='append')
    a = p.parse_args(); plan = json.loads((BASE.parent / 'plan.json').read_text())
    a.output = a.output.resolve(); a.output.mkdir(parents=True, exist_ok=False)
    for name, expected in plan['final_tool_hashes'].items():
        assert digest(BASE / name) == expected, name
    cases = [c for c in plan['cases'] if a.case is None or c['case'] in a.case]
    assert cases and (a.case is None or set(a.case) == {c['case'] for c in cases}), 'Unknown case'
    actual = {'native_sha256': digest(a.native), 'java_jar_sha256': digest(a.java_jar)}
    replay = {'method': 'New replay, not the original published trial arrays', 'actual_artifacts': actual,
              'matches_original_native': actual['native_sha256'] == plan['native_candidate']['sha256'],
              'matches_original_java_jar': actual['java_jar_sha256'] == plan['target']['java_jar_sha256'],
              'runtime_environment': {k: os.environ.get(k) for k in ['GOGC', 'GOMEMLIMIT', 'GOMAXPROCS', 'GODEBUG', 'JAVA_TOOL_OPTIONS', 'JDK_JAVA_OPTIONS', '_JAVA_OPTIONS']},
              'results': []}
    report = a.output / 'replay-summary.json'
    def save(): report.write_text(json.dumps(replay, indent=2) + '\n')
    save()
    for case in cases:
        if shutil.disk_usage(a.output).free < 300 * 1024 * 1024: raise RuntimeError('Less than300MiB free')
        data = a.inputs / NAMES[case['dataset']]
        assert digest(data) == plan['datasets'][case['dataset']]['sha256'], str(data)
        target = a.output / case['case']
        command = [sys.executable, str(BASE / 'tools/benchmark.py'), '--go', str(a.native.resolve()),
                   '--go-tool', a.go_tool, '--java', a.java, '--java-jar', str(a.java_jar.resolve()),
                   '--input', str(data.resolve()), '--input-pool', str(a.output / 'input-pool'),
                   '--repeat', str(case['measured_pairs']), '--warmup', '1',
                   '--event-mode', case.get('event_mode', 'cpu'), '--format', case['format'],
                   '--repeat-inputs', str(case.get('repeat_inputs', 1)), '--workdir', str(target)]
        command += ['--flag=' + f for f in case['flags']]
        command += ['--go-flag=' + f for f in case.get('native_flags', [])]
        if case.get('additional_tree_rss_diagnostic_pairs'):
            command += ['--tree-rss-runs', str(case['additional_tree_rss_diagnostic_pairs'])]
        result = subprocess.run(command)
        replay['results'].append({'case': case['case'], 'returncode': result.returncode, 'command': command})
        save()
        if result.returncode: raise SystemExit(result.returncode)


if __name__ == '__main__': main()
