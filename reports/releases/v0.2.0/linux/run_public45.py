#!/usr/bin/env python3
"""Run exact-tag assertions, plus a required native latency differential."""
from pathlib import Path
import argparse
import hashlib
import json
import os
import re
import subprocess
import time
ROOT=Path(__file__).resolve().parents[2]
FILTERS=['JfrTests.cpuLoad','JfrTests.recordCpuMultiEngine','JfrTests.ttsp','StackwalkerTests','NativememTests.ldpreload','NativelockTests.ldpreloadAllLockTypes','NativelockTests.nativeAllLockContention','JfrconverterTests','OtlpTests.periodFromJfr','OtlpTests.threadNameFromJfr','OtlpTests.samplesFromJfr','OtlpTests.nonAggregatedSamplesFromJfr','OtlpTests.profileTimeFromJfr','SpanTests']

def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--native',type=Path)
    parser.add_argument('--java',default='/opt/homebrew/opt/openjdk@21/bin/java')
    parser.add_argument('--reference-jar',type=Path,default=ROOT/'work/async-profiler-4.5/build/jar/jfr-converter.jar')
    parser.add_argument('--source-manifest',type=Path,default=ROOT/'work/go-v45/upstream-source.json')
    parser.add_argument('--scope-class-dir',type=Path,default=ROOT/'work/go-v45')
    args=parser.parse_args()
    stage=args.stage.resolve();out=args.output.resolve();out.mkdir(parents=True,exist_ok=False)
    pristine=json.loads(args.source_manifest.read_text())
    assertions={k:v for k,v in pristine['files'].items() if k.startswith('test/test/') and k.endswith('.java')}
    for name,expected in assertions.items():assert sha(stage/name)==expected,name
    classpath='build/test.jar:build/jar/*:build/lib/*:test/deps/*:test/gen/*'
    scope=subprocess.check_output([args.java,'-cp',str(args.scope_class_dir.resolve())+':'+classpath,'PublicScope',*FILTERS],cwd=stage,text=True)
    (out/'scope.tsv').write_text(scope)
    assert len(scope.splitlines())==27,len(scope.splitlines())
    command=[args.java,'-ea','-Djava.library.path=build/test/lib','-DthreadCount=1','-DretryCount=0','-DlogDir='+str(out/'profiles'),'-cp',classpath,'one.profiler.test.Runner',*FILTERS]
    environment=dict(os.environ)
    if args.native:environment['JFRCONV_BINARY']=str(args.native.resolve())
    started=time.perf_counter()
    with (out/'tests.log').open('w') as log:
        result=subprocess.run(command,cwd=stage,env=environment,stdout=log,stderr=subprocess.STDOUT)
    rows=[]
    for line in (out/'tests.log').read_text(errors='replace').splitlines():
        match=re.match(r'^(PASS|FAIL|SKIP_[A-Z_]+) \[(\d+)/(\d+)\] (.*) took ([0-9.,]+) s$',line)
        if match:rows.append({'status':match[1],'test':match[4],'seconds':float(match[5].replace(',','.'))})
    counts={}
    for row in rows:counts[row['status']]=counts.get(row['status'],0)+1
    report={'release':'v4.5','commit':pristine['commit'],'stage':str(stage),'assertion_files_unchanged':True,'assertion_sources':assertions,'runner_sha256':sha(__file__),'transport':('native CLI / native OTLP bytes; original Java latency visitor supplemented by required native differential' if args.native else 'pristine original Java4.5'),'native_binary':str(args.native.resolve()) if args.native else None,'native_binary_sha256':sha(args.native) if args.native else None,'java_executable':args.java,'java_version':subprocess.run([args.java,'-version'],capture_output=True,text=True).stderr.strip(),'command':command,'filters':FILTERS,'declared_test_count':27,'scope_file':'scope.tsv','returncode':result.returncode,'elapsed_seconds':time.perf_counter()-started,'counts':counts,'tests':rows}
    effective=result.returncode
    if args.native and result.returncode==0:
        try:
            from latency_native_oracle import run as native_latency
            tracer=(stage/'test/test/jfrconverter/Tracer.java').read_text()
            minimum=int(re.search(r'TRACE_DURATION_MS\s*=\s*(\d+)',tracer)[1])-10
            recordings=list((out/'profiles').glob('JfrconverterTests.latencyFilter*/f.jfr'))
            assert len(recordings)==1,recordings
            extra=native_latency(args.java,args.reference_jar.resolve(),recordings[0],args.native.resolve(),minimum,out/'latency-native-oracle')
            report['latency_native_differential']=extra
            if not extra['passed']:effective=1
        except Exception as error:
            report['latency_native_differential']={'passed':False,'error':type(error).__name__+': '+str(error)}
            effective=1
    report['effective_returncode']=effective
    (out/'public-tests.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps({'returncode':effective,'counts':counts,'report':str(out/'public-tests.json')},indent=2))
    raise SystemExit(effective)

if __name__=='__main__':main()
