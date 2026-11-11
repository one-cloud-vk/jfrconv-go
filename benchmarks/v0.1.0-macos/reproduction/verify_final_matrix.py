"""Independently recompute final matrix statistics from retained raw trials."""
from pathlib import Path
import argparse
import hashlib
import json
import math
import statistics

ROOT = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser()
parser.add_argument('--matrix', type=Path, default=ROOT / 'work/native-benchmark-macos-final/matrix-summary.json')
parser.add_argument('--output', type=Path, default=ROOT / 'outputs/final-matrix-audit.json')
args = parser.parse_args()
matrix_path = args.matrix
matrix = json.loads(matrix_path.read_text())
assert matrix['status'] == 'complete', matrix['status']
rows = matrix['results']
assert len(rows) == 58
assert len({(r['implementation'], r['case']) for r in rows}) == 58
summary = {}


def close(a, b):
    assert math.isclose(a, b, rel_tol=1e-12, abs_tol=1e-12), (a, b)


for native in ['go', 'rust']:
    native_rows = [r for r in rows if r['implementation'] == native]
    assert len(native_rows) == 29
    report = {'cases': 29, 'pairs_per_case': 21, 'measured_processes': 29 * 21 * 2, 'rss_wins': 0, 'p95_rss_wins': 0, 'p50_wins': 0, 'p95_wins': 0, 'losses': [], 'examples': {}}
    reductions, p50_ratios, p95_ratios = [], [], []
    for row in native_rows:
        assert row['returncode'] == 0 and row['report_status'] == 'complete' and row['repeat'] == 21
        native_key = 'go' if native == 'go' else 'rust'
        assert len(row['recordings']) == 1
        r = row['recordings'][0]
        assert r['output_matches']
        verification = r['output_verification']
        assert len(verification['measured_pairs']) == 21
        assert all(v['output_matches'] for v in verification['measured_pairs'])
        assert len(verification['warmup_pairs']) == 1 and verification['warmup_pairs'][0]['output_matches']
        assert len(r['execution_order']) == 21
        for i, order in enumerate(r['execution_order']):
            assert set(order) == {'java', native_key}
            if i:
                assert order == list(reversed(r['execution_order'][i - 1]))
        for name in ['java', native_key]:
            trials = r['runs'][name]
            assert len(trials) == 21 and all(t['exit_code'] == 0 for t in trials)
            assert len(r['warmup_runs'][name]) == 1 and r['warmup_runs'][name][0]['exit_code'] == 0
            assert r['first_fresh_process'][name]['exit_code'] == 0
            for metric, raw in [('peak_rss_bytes', 'peak_rss_bytes'), ('wall_seconds', 'wall_seconds')]:
                values = sorted(t[raw] for t in trials)
                close(r[name]['median_' + metric], statistics.median(values))
                close(r[name]['p95_' + metric], values[math.ceil(.95 * len(values)) - 1])
                close(r[name]['max_' + metric], max(values))
        j, n = r['java'], r[native_key]
        rss = (1 - n['median_peak_rss_bytes'] / j['median_peak_rss_bytes']) * 100
        p50 = j['median_wall_seconds'] / n['median_wall_seconds']
        p95 = j['p95_wall_seconds'] / n['p95_wall_seconds']
        close(r['rss_reduction_percent'], rss)
        close(r['latency_p50_speedup_ratio'], p50)
        close(r['latency_p95_speedup_ratio'], p95)
        report['rss_wins'] += int(rss > 0)
        report['p95_rss_wins'] += int(n['p95_peak_rss_bytes'] < j['p95_peak_rss_bytes'])
        report['p50_wins'] += int(p50 > 1)
        report['p95_wins'] += int(p95 > 1)
        reductions.append(rss); p50_ratios.append(p50); p95_ratios.append(p95)
        if rss <= 0 or p50 <= 1 or p95 <= 1:
            report['losses'].append({'case': row['case'], 'rss_reduction_percent': rss, 'p50_speedup': p50, 'p95_speedup': p95, 'java': j, native: n})
        if row['case'] in ['startup-100k', 'throughput-10m', 'diverse-2m-collapsed', 'deep-shared-cp']:
            report['examples'][row['case']] = {'java': j, native: n, 'rss_reduction_percent': rss, 'p50_speedup': p50, 'p95_speedup': p95}
    report['rss_reduction_percent_range'] = [min(reductions), max(reductions)]
    report['p50_speedup_range'] = [min(p50_ratios), max(p50_ratios)]
    report['p95_speedup_range'] = [min(p95_ratios), max(p95_ratios)]
    summary[native] = report

result = {'method': 'Independent recomputation of all medians, nearest-rank p95, maxima and ratios from all 58 retained 21-pair raw result arrays; no process executions.', 'matrix_sha256': hashlib.sha256(matrix_path.read_bytes()).hexdigest(), 'results': summary}
out = args.output
out.write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result, indent=2))
