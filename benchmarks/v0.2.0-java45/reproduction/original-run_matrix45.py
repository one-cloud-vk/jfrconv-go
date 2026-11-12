#!/usr/bin/env python3
"""Execute the declaration-time nine-case pinned Java4.5/native comparison."""
from pathlib import Path
import datetime,hashlib,json,os,shutil,subprocess,sys,time
ROOT=Path.cwd();HOME=ROOT/'work/go-v45';PLAN=HOME/'performance-plan.json';OUT=HOME/'performance-final'
def sha(p):
    h=hashlib.sha256()
    with Path(p).open('rb') as f:
        for b in iter(lambda:f.read(65536),b''):h.update(b)
    return h.hexdigest()
def main():
    plan=json.loads(PLAN.read_text());candidate=plan['native_candidate'];reference=plan['target']
    assert sha(candidate['path'])==candidate['sha256'];assert sha(reference['java_jar'])==reference['java_jar_sha256']
    assert sha(candidate['source_manifest'])==candidate['source_manifest_sha256']
    if (OUT/'matrix-summary.json').exists():raise RuntimeError('Refusing to replace an existing official matrix')
    OUT.mkdir(exist_ok=True)
    report={'status':'running','target':'pristine Java jfrconv4.5 versus native Go0.2.0','release':'v4.5','commit':reference['commit'],'candidate':candidate,'java_reference':reference,'plan_sha256':sha(PLAN),'started_UTC':datetime.datetime.now(datetime.timezone.utc).isoformat(),'runtime_environment':{k:os.environ.get(k) for k in ['GOGC','GOMEMLIMIT','GOMAXPROCS','GODEBUG','JAVA_TOOL_OPTIONS','JDK_JAVA_OPTIONS','_JAVA_OPTIONS']},'tool_hashes':plan['final_tool_hashes'],'results':[]}
    def persist():
        p=OUT/'matrix-summary.json';tmp=p.with_suffix('.json.tmp');tmp.write_text(json.dumps(report,indent=2)+'\n');tmp.replace(p)
    persist()
    try:
        for case in plan['cases']:
            if shutil.disk_usage(OUT).free<300*1024*1024:raise RuntimeError('Disk headroom below300MiB before next row; preserve all trials and pause')
            for relative,digest in plan['final_tool_hashes'].items():assert sha(ROOT/'outputs/jfrconv-go'/relative)==digest,relative
            data=plan['datasets'][case['dataset']];assert sha(data['path'])==data['sha256']
            directory=OUT/case['case'];directory.mkdir(exist_ok=False)
            command=[sys.executable,str(ROOT/'outputs/jfrconv-go/tools/benchmark.py'),'--go',candidate['path'],'--go-tool',str(ROOT/'work/toolchains/go1.27.4/go/bin/go'),'--java',reference['java_executable'],'--java-jar',reference['java_jar'],'--input',data['path'],'--input-pool',str(ROOT/'work/native-benchmark-macos-final/frozen-inputs'),'--repeat',str(case['measured_pairs']),'--warmup','1','--event-mode',case.get('event_mode','cpu'),'--format',case['format'],'--repeat-inputs',str(case.get('repeat_inputs',1)),'--workdir',str(directory)]
            command.extend('--flag='+f for f in case['flags']);command.extend('--go-flag='+f for f in case.get('native_flags',[]))
            if case.get('additional_tree_rss_diagnostic_pairs'):command+=['--tree-rss-runs',str(case['additional_tree_rss_diagnostic_pairs'])]
            report['active_case']={'case':case['case'],'command':command,'started_UTC':datetime.datetime.now(datetime.timezone.utc).isoformat()};persist();started=time.perf_counter()
            with (directory/'harness.log').open('w') as log:result=subprocess.run(command,cwd=ROOT,stdout=log,stderr=subprocess.STDOUT)
            row={'case':case['case'],'returncode':result.returncode,'elapsed_harness_seconds':time.perf_counter()-started,'report':str(directory/'benchmark.json'),'log':str(directory/'harness.log')}
            if (directory/'benchmark.json').exists():
                raw=json.loads((directory/'benchmark.json').read_text());row.update({'report_status':raw['status'],'repeat':raw['repeat'],'recordings':raw['recordings']})
            report['results'].append(row);persist()
            if result.returncode:raise RuntimeError('Official row failed; retain report/log and prior rows: '+case['case'])
            r=row['recordings'][0];row['losses']=[]
            if r['go']['median_peak_rss_bytes']>=r['java']['median_peak_rss_bytes']:row['losses'].append('median_RSS')
            if r['go']['p95_peak_rss_bytes']>=r['java']['p95_peak_rss_bytes']:row['losses'].append('p95_RSS')
            for key in ['latency_p50_speedup_ratio','latency_p95_speedup_ratio']:
                if r[key]<1:row['losses'].append(key)
            persist();print(case['case'],json.dumps({k:r[k] for k in ['rss_reduction_percent','latency_p50_speedup_ratio','latency_p95_speedup_ratio']}),'LOSSES='+','.join(row['losses']),flush=True)
        assert sha(candidate['source_manifest'])==candidate['source_manifest_sha256']
        report['status']='complete';report.pop('active_case',None)
    except BaseException as error:
        report['status']='failed';report['error']={'type':type(error).__name__,'message':str(error)};raise
    finally:persist()
    print(OUT/'matrix-summary.json',flush=True)
if __name__=='__main__':main()
