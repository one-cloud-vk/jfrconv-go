#!/usr/bin/env python3
"""Compare native/Java filters on recordings made by official4.5 Span APIs."""
from pathlib import Path
from collections import Counter
import base64
import hashlib
import json
import subprocess

work = Path('/task')
output = work / 'results/actual-span-api'
output.mkdir(exist_ok=False)
binary = work / 'bin/jfrconv'
jar = work / 'reference/jfr-converter.jar'
java = '/opt/java/openjdk/bin/java'
digest = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
recordings = {name: work / 'results/public-native/profiles' / ('SpanTests.' + name) / 'f.jfr' for name in ('api', 'spans', 'attach')}
metadata = {}
for name, recording in recordings.items():
    result = subprocess.check_output([java, '-cp', str(work / 'extra-classes'), 'SpanInspect', str(recording)], text=True)
    (output / (name + '-jdk-events.tsv')).write_text(result)
    tags, samples = [], 0
    for line in result.splitlines():
        parts = line.split('\t')
        if parts[0] == 'SPAN':
            tags.append({'tag': None if parts[1] == '-' else base64.b64decode(parts[1]).decode(), 'duration_ns': int(parts[2])})
        elif parts[0] == 'SAMPLES':
            samples = int(parts[1])
    metadata[name] = {'recording_sha256': digest(recording), 'recording_bytes': recording.stat().st_size, 'span_events': tags, 'execution_events': samples}
assert [event['tag'] for event in metadata['api']['span_events']] == ['duringSession']
assert metadata['api']['execution_events'] > 0
cases = [
    ('api-all', 'api', [], True),
    ('api-during', 'api', ['--tag', 'duringSession'], True),
    ('api-before-session-absent', 'api', ['--tag', 'beforeSession'], False),
    ('api-after-session-absent', 'api', ['--tag', 'afterSession'], False),
    ('api-literal-not-regex', 'api', ['--tag', 'during.*'], False),
    ('api-latency250ms', 'api', ['--latency', '250ms'], True),
    ('api-latency400ms', 'api', ['--latency', '400ms'], False),
    ('spans-busy', 'spans', ['--tag', 'busyRequest'], True),
    ('spans-optional', 'spans', ['--tag', 'busyOptional'], True),
    ('spans-null-is-not-empty', 'spans', ['--tag', ''], False),
]
rows = []
for name, recording_name, flags, expected_nonempty in cases:
    maps, commands = {}, {}
    for implementation, base in [('java', [java, '-jar', str(jar)]), ('native', [str(binary)])]:
        target = output / (name + '.' + implementation + '.collapsed')
        target.unlink(missing_ok=True)
        command = base + ['--cpu', *flags, '-o', 'collapsed', str(recordings[recording_name]), str(target)]
        result = subprocess.run(command, capture_output=True, text=True)
        (output / (name + '.' + implementation + '.stdout')).write_text(result.stdout)
        (output / (name + '.' + implementation + '.stderr')).write_text(result.stderr)
        assert result.returncode == 0, (name, implementation, result.stderr)
        values = Counter()
        for line in target.read_text().splitlines():
            stack, amount = line.rsplit(' ', 1)
            values[stack] += int(amount)
        maps[implementation], commands[implementation] = +values, command
    equal = maps['java'] == maps['native']
    total = sum(maps['native'].values())
    rows.append({'case': name, 'flags': ['--cpu', *flags], 'recording': recording_name, 'same_full_stack_count_map': equal, 'java_total': sum(maps['java'].values()), 'native_total': total, 'expected_nonempty': expected_nonempty, 'commands': commands})
    assert equal and (total > 0) == expected_nonempty, rows[-1]
report = {'status': 'PASS', 'upstream_commit': '11aaea310cb75843f0e7532365d8cedc8d0e14c8', 'source': 'Unchanged official SpanTests.api/spans/attach recordings from this Linux public test run; no synthetic event substitution.', 'native_binary_sha256': digest(binary), 'java_jar_sha256': digest(jar), 'jdk_reader_inspector_sha256': digest(work / 'SpanInspect.java'), 'recordings': metadata, 'cases': rows, 'passed': len(rows)}
(output / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps({'status': 'PASS', 'cases': len(rows), 'api_span_tag': metadata['api']['span_events'][0]['tag']}, indent=2))
