#!/usr/bin/env python3
"""Run unchanged published assertions through an existing generic native adapter."""
from pathlib import Path
import hashlib,json,os,re,shutil,subprocess,time
ROOT=Path.cwd()
JAVA='/opt/homebrew/opt/openjdk@21/bin/java'
OUT=ROOT/'work/go-rss-followup/public-macos-jdk21'
BINARIES={'go':(ROOT/'work/go-rss-followup/candidate/jfrconv','785dfc5fd23cf07dae4e26f905d2699ee8c128a63e3256b7b1a255428170d6de')}
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def assertions(stage):
    manifest=json.loads((stage/'public-test-manifest.json').read_text())
    for name,digest in manifest['assertion_sources'].items():assert sha(stage/name)==digest,name
    return manifest

def main():
    OUT.mkdir(parents=True,exist_ok=False)
    summary={'status':'running','platform':os.uname().sysname+' '+os.uname().machine,'results':[],'baseline_java':[]}
    def persist():(OUT/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
    for suite in ['v41','current']:
        stage=ROOT/f'work/upstream-macos/{suite}-java';manifest=assertions(stage)
        old=json.loads((stage/'public-test-results/public-tests.json').read_text());assert old['revision']==manifest['revision'] and old['assertion_files_unchanged'] and old['returncode']==0
        copied=OUT/f'baseline-{suite}-java.json';copied.write_text(json.dumps(old,indent=2)+'\n')
        summary['baseline_java'].append({'suite':suite,'revision':manifest['revision'],'source_assertions_reverified':True,'reused_report':str(copied),'original_report_sha256':sha(stage/'public-test-results/public-tests.json'),'note':'Historical source-unchanged Java baseline, runtime/version and date retained in copied original report.'})
    persist()
    try:
        for impl,(source,expected) in BINARIES.items():
            assert sha(source)==expected
            native_dir=OUT/impl;native_dir.mkdir()
            binary=native_dir/'jfrconv-measured';shutil.copyfile(source,binary);binary.chmod(0o555)
            for suite,filters in [('v41',['JfrTests.regularPeak','JfrTests.ttsp','StackwalkerTests','NativememTests.ldpreload']),('current',['JfrconverterTests'])]:
                stage=ROOT/f'work/upstream-macos/{suite}-rust';manifest=assertions(stage)
                directory=native_dir/suite;directory.mkdir();profiles=directory/'profiles'
                command=[JAVA,'-ea','-Djava.library.path=build/test/lib','-DthreadCount=1','-DretryCount=0','-DlogDir='+str(profiles),'-cp','build/test.jar:build/jar/*:build/lib/*:test/deps/*:test/gen/*','one.profiler.test.Runner',*filters]
                bridge=stage/'src/converter/one/convert/RustBridge.java';bridge_text=bridge.read_text()
                assert 'cli.add(input); cli.add(output);' in bridge_text
                assert '"/dev/null"' not in bridge_text
                env=dict(os.environ,JFRCONV_BINARY=str(binary));started=time.perf_counter()
                with (directory/'tests.log').open('w') as output:
                    result=subprocess.run(command,cwd=stage,stdout=output,stderr=subprocess.STDOUT,env=env)
                text=(directory/'tests.log').read_text(errors='replace');rows=[]
                for line in text.splitlines():
                    m=re.match(r'^(PASS|FAIL|SKIP_[A-Z_]+) \[(\d+)/(\d+)\] (.*) took ([0-9.,]+) s$',line)
                    if m:rows.append({'status':m[1],'test':m[4],'seconds':float(m[5].replace(',','.'))})
                counts={}
                for row in rows:counts[row['status']]=counts.get(row['status'],0)+1
                assertions(stage)
                report={'implementation':'native Go','version':'0.1.1','native_binary':str(binary),'native_binary_sha256':sha(binary),'revision':manifest['revision'],'suite':'v4.1' if suite=='v41' else suite,'assertion_files_unchanged':True,'assertion_sources':manifest['assertion_sources'],'transport_bridge':True,'transport_note':'Reused compiled generic transport has historical class name RustBridge. JFRCONV_BINARY invokes the recorded native executable directly; no Rust or Java implementation is used by the Go application.','bridge_source_sha256':sha(bridge),'direct_requested_output_paths':True,'dev_null_handling':'Passed unchanged to native CLI, without temporary substitution.','command':command,'working_directory':str(stage),'returncode':result.returncode,'elapsed_seconds':time.perf_counter()-started,'counts':counts,'tests':rows,'java_version':subprocess.run([JAVA,'-version'],capture_output=True,text=True).stderr.strip()}
                (directory/'public-tests.json').write_text(json.dumps(report,indent=2)+'\n');summary['results'].append({'implementation':impl,'suite':suite,'report':str(directory/'public-tests.json'),'returncode':result.returncode,'counts':counts});persist()
                print(impl,suite,'returncode',result.returncode,counts,flush=True)
                expected_pass=8 if suite=='v41' else 6
                if result.returncode!=0 or counts.get('PASS')!=expected_pass:raise RuntimeError(f'{impl}/{suite} public suite failed; preserve logs and investigate')
        summary['status']='complete'
    except BaseException as error:
        summary['status']='failed';summary['error']={'type':type(error).__name__,'message':str(error)};raise
    finally:persist()
if __name__=='__main__':main()
