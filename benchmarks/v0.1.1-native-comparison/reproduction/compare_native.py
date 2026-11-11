#!/usr/bin/env python3
"""Pair two frozen native Go releases; no Java/Rust application shim."""
from __future__ import annotations
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as source:
        for block in iter(lambda: source.read(65536), b''):
            h.update(block)
    return h.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', required=True, type=Path)
    parser.add_argument('--baseline', required=True, type=Path)
    parser.add_argument('--candidate', required=True, type=Path)
    parser.add_argument('--candidate-sha256', required=True)
    parser.add_argument('--workdir', required=True, type=Path)
    parser.add_argument('--helpers', type=Path, default=Path('outputs/jfrconv-go/tools/benchmark.py'))
    parser.add_argument('--go-tool', default='go')
    args = parser.parse_args()
    args.workdir = args.workdir.resolve()
    if (args.workdir / 'comparison.json').exists():
        raise RuntimeError('Refusing to overwrite an existing comparison')
    args.workdir.mkdir(parents=True, exist_ok=True)
    plan = json.loads(args.plan.read_text())
    environment = {k: os.environ.get(k) for k in ['GOGC','GOMEMLIMIT','GOMAXPROCS','GODEBUG']}
    if 'runtime_environment' in plan:
        assert environment == plan['runtime_environment'], 'Runtime policy differs from the declared plan'
    helpers = args.helpers.resolve()
    sys.path.insert(0, str(helpers.parent))
    spec = importlib.util.spec_from_file_location('benchmark_measurement_helpers', helpers)
    shared = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(shared)
    oracle_paths = [helpers.with_name('heatmap_decode.js'), helpers.with_name('heatmap_colors.js'),
                    helpers.parent.parent / 'assets/heatmap.html']
    metadata = {}
    binaries = {}
    for name, original, expected in [('old', args.baseline, plan['baseline']['sha256']),
                                     ('new', args.candidate, args.candidate_sha256)]:
        assert digest(original) == expected, f'{name} source binary hash changed'
        target = args.workdir / 'binaries' / name
        target.parent.mkdir(exist_ok=True)
        shutil.copyfile(original, target)
        target.chmod(0o555)
        assert digest(target) == expected
        binaries[name] = target
        metadata[name] = {'source': str(original.resolve()), 'frozen_binary': str(target),
                          'sha256': expected,
                          'build_info': subprocess.run([args.go_tool, 'version', '-m', str(target)],
                                                       capture_output=True, text=True).stdout.strip()}
    report = {'schema': 'jfrconv-native-old-new-v1', 'status': 'running',
              'purpose': plan['objective'], 'plan_sha256': digest(args.plan),
              'runner_sha256': digest(Path(__file__)),
              'measurement_helpers': {'source': str(helpers), 'sha256': digest(helpers)},
              'heatmap_oracles': {str(p): digest(p) for p in oracle_paths},
              'binaries': metadata, 'protocol': plan['protocol'],
              'runtime_environment': environment,
              'cases': []}
    output = args.workdir / 'comparison.json'
    def persist():
        temporary = output.with_suffix('.json.tmp')
        temporary.write_text(json.dumps(report, indent=2) + '\n')
        temporary.replace(output)
    def verify(case, paths):
        if case['format'] == 'collapsed':
            old, new = (shared.canonical(paths[k]) for k in ['old','new'])
            proof = {'output_matches': old == new, 'stack_count': len(old),
                     'sample_total': sum(old.values()),
                     'canonical_sha256': hashlib.sha256(json.dumps(sorted(old.items()),
                         ensure_ascii=False, separators=(',',':')).encode()).hexdigest()}
        elif case['format'] == 'heatmap':
            decoder, colors = helpers.with_name('heatmap_decode.js'), helpers.with_name('heatmap_colors.js')
            old, new = [json.loads(subprocess.check_output(['node', str(decoder), str(paths[k])], text=True)) for k in ['old','new']]
            result = subprocess.run(['node', str(colors), str(paths['old']), str(paths['new'])], capture_output=True, text=True)
            rgb = json.loads(result.stdout)
            same_rgb = (result.returncode == 0 and rgb['same_used_method_IDs_RGB']
                        and rgb['java_digest'] == rgb['native_digest'])
            proof = {'output_matches': old == new and same_rgb, 'timeline': old,
                     'used_method_IDs_RGB_match': same_rgb, 'used_methods': rgb['java_methods'],
                     'old_method_RGB_digest': rgb['java_digest'], 'new_method_RGB_digest': rgb['native_digest']}
            if not same_rgb:
                proof['color_differences'] = rgb
        else:
            raise ValueError('Followup plan supports collapsed and heatmap formats')
        proof['raw_output_sha256'] = {k: digest(v) for k,v in paths.items()}
        return proof
    persist()
    try:
        for index, case in enumerate(plan['cases']):
            if shutil.disk_usage(args.workdir).free < 300*1024*1024:
                raise RuntimeError('Disk headroom below300MiB; stop and retain all prior trials')
            directory = args.workdir / case['case']
            directory.mkdir()
            source_data = plan['datasets'][case['dataset']]
            original = Path(source_data['path'])
            assert digest(original) == source_data['sha256']
            source = directory / 'inputs' / original.name
            input_sha, pool_file = shared.freeze_input(original, Path(plan['input_pool']), source)
            assert input_sha == source_data['sha256'], 'Input changed between declaration and freezing'
            row = {'case': case['case'], 'format': case['format'], 'pairs': case['measured_pairs'],
                   'input': str(source), 'input_sha256': input_sha, 'input_pool_file': str(pool_file),
                   'first_fresh_process': {}, 'warmup_runs': {'old': [], 'new': []},
                   'runs': {'old': [], 'new': []}, 'execution_order': [],
                   'verification': {'first': [], 'warmup': [], 'measured': [], 'tree_rss': []},
                   'commands': {}}
            report['cases'].append(row)
            paths = {}
            for name, binary in binaries.items():
                target = directory / (name + '.' + case['format'])
                paths[name] = target
                if case.get('repeat_inputs', 1) > 1:
                    target = directory / (name + '-outputs')
                    target.mkdir()
                    paths[name] = target / (source.stem + '.' + ('html' if case['format']=='heatmap' else case['format']))
                mode = [] if case.get('event_mode','cpu')=='all' else ['--'+case.get('event_mode','cpu')]
                row['commands'][name] = [str(binary), *mode, *case['flags'], *case.get('native_flags',[]),
                    '-o', case['format'], *([str(source)]*case.get('repeat_inputs',1)), str(target)]
            def trial(name, phase, iteration=None, tree=False):
                paths[name].unlink(missing_ok=True)
                report['active'] = {'case':case['case'], 'phase':phase, 'iteration':iteration, 'binary':name}
                persist()
                error = None
                try:
                    result = (shared.measure_tree_rss if tree else shared.measure)(row['commands'][name])
                except shared.MeasurementError as failure:
                    result, error = failure.result, failure
                if phase=='first':
                    row['first_fresh_process'][name] = result
                else:
                    collection = {'warmup':'warmup_runs','measured':'runs','tree_rss':'tree_rss_diagnostics'}[phase]
                    row[collection][name].append(result)
                persist()
                if error:
                    raise error
                if tree and not result['complete']:
                    raise RuntimeError('Tree RSS monitor incomplete; see retained raw diagnostic')
            def pair(order, phase, iteration=None, tree=False):
                for name in order:
                    trial(name, phase, iteration, tree)
                report['active'] = {'case':case['case'], 'phase':phase+'-verification','iteration':iteration}
                proof = verify(case, paths)
                row['verification'][phase].append(proof)
                persist()
                if not proof['output_matches']:
                    raise AssertionError('Old/new semantic or exact-RGB output mismatch')
            order = ['old','new'] if index%2==0 else ['new','old']
            pair(order, 'first')
            pair(order, 'warmup', 0)
            for iteration in range(case['measured_pairs']):
                current = order if iteration%2==0 else list(reversed(order))
                row['execution_order'].append(current)
                pair(current, 'measured', iteration)
            count = case.get('additional_tree_rss_diagnostic_pairs',0)
            if count:
                row['tree_rss_diagnostics'] = {'old':[],'new':[]}
                for iteration in range(count):
                    current = order if iteration%2==0 else list(reversed(order))
                    pair(current,'tree_rss',iteration,tree=True)
            assert digest(source)==input_sha
            for name in ['old','new']:
                row[name] = shared.summarize(row['runs'][name])
            old, new = row['old'], row['new']
            row['median_RSS_reduction_percent'] = 100*(1-new['median_peak_rss_bytes']/old['median_peak_rss_bytes'])
            row['p95_RSS_reduction_percent'] = 100*(1-new['p95_peak_rss_bytes']/old['p95_peak_rss_bytes'])
            row['p50_speedup'] = old['median_wall_seconds']/new['median_wall_seconds']
            row['p95_speedup'] = old['p95_wall_seconds']/new['p95_wall_seconds']
            persist()
            print(case['case'], 'RSS reduction', round(row['median_RSS_reduction_percent'],2),
                  'p50/p95',round(row['p50_speedup'],3),round(row['p95_speedup'],3),flush=True)
        assert digest(helpers)==report['measurement_helpers']['sha256']
        for path, expected in report['heatmap_oracles'].items():
            assert digest(Path(path)) == expected
        for name, path in binaries.items():
            assert digest(path)==metadata[name]['sha256']
        report['status'] = 'complete'
        report.pop('active',None)
    except BaseException as failure:
        report['status'] = 'failed'
        report['error'] = {'type':type(failure).__name__,'message':str(failure)}
        raise
    finally:
        persist()
    print(output,flush=True)

if __name__=='__main__':
    main()
