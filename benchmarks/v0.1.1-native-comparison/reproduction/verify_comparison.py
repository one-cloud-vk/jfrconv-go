#!/usr/bin/env python3
"""Recompute every retained old/new statistic without running applications."""
import argparse
import hashlib
import json
import math
import statistics
from pathlib import Path

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--input',required=True,type=Path)
parser.add_argument('--output',required=True,type=Path)
args=parser.parse_args()
a=json.loads(args.input.read_text())
assert a['status']=='complete' and len(a['cases'])==6
assert len({r['case'] for r in a['cases']})==6
rows=[]
def equal(actual,expected):
    assert math.isclose(actual,expected,rel_tol=1e-12,abs_tol=1e-12),(actual,expected)
for r in a['cases']:
    assert r['pairs']==21 and len(r['execution_order'])==21
    assert len(r['verification']['first'])==len(r['verification']['warmup'])==1
    assert len(r['verification']['measured'])==21
    assert all(p['output_matches'] for proofs in r['verification'].values() for p in proofs)
    for i,order in enumerate(r['execution_order']):
        assert set(order)=={'old','new'}
        if i:assert order==list(reversed(r['execution_order'][i-1]))
    for name in ['old','new']:
        assert r['first_fresh_process'][name]['exit_code']==0
        assert len(r['warmup_runs'][name])==1 and r['warmup_runs'][name][0]['exit_code']==0
        trials=r['runs'][name];assert len(trials)==21 and all(t['exit_code']==0 for t in trials)
        for raw in ['peak_rss_bytes','wall_seconds']:
            values=sorted(t[raw] for t in trials)
            for prefix,value in [('median',statistics.median(values)),('p95',values[math.ceil(.95*len(values))-1]),('max',max(values))]:
                equal(r[name][prefix+'_'+raw],value)
    o,n=r['old'],r['new']
    metrics={'median_RSS_reduction_percent':100*(1-n['median_peak_rss_bytes']/o['median_peak_rss_bytes']),
             'p95_RSS_reduction_percent':100*(1-n['p95_peak_rss_bytes']/o['p95_peak_rss_bytes']),
             'p50_speedup':o['median_wall_seconds']/n['median_wall_seconds'],
             'p95_speedup':o['p95_wall_seconds']/n['p95_wall_seconds']}
    for key,value in metrics.items():equal(r[key],value)
    if r['format']=='heatmap':
        for proofs in r['verification'].values():
            assert all(p['used_method_IDs_RGB_match'] and p['old_method_RGB_digest']==p['new_method_RGB_digest'] for p in proofs)
    if 'tree_rss_diagnostics' in r:
        assert len(r['verification']['tree_rss'])==5
        for name in ['old','new']:
            values=r['tree_rss_diagnostics'][name];assert len(values)==5
            assert all(t['complete'] and not t['included_in_latency_statistics'] and not t['monitor_errors'] for t in values)
    rows.append({'case':r['case'],**metrics})
result={'method':'Recomputed all medians, nearest-rank p95, maxima and ratios from raw21-pair arrays; no application execution.',
        'comparison_sha256':hashlib.sha256(args.input.read_bytes()).hexdigest(),
        'baseline_sha256':a['binaries']['old']['sha256'],'candidate_sha256':a['binaries']['new']['sha256'],
        'cases':rows,'rows':len(rows),'pairs_per_case':21,
        'median_RSS_lower':sum(r['median_RSS_reduction_percent']>0 for r in rows),
        'median_RSS_equal':sum(r['median_RSS_reduction_percent']==0 for r in rows),
        'p95_RSS_lower':sum(r['p95_RSS_reduction_percent']>0 for r in rows),
        'p50_faster':sum(r['p50_speedup']>1 for r in rows),'p95_faster':sum(r['p95_speedup']>1 for r in rows)}
args.output.write_text(json.dumps(result,indent=2)+'\n')
print('All6 cases and raw21-pair statistics verified')
