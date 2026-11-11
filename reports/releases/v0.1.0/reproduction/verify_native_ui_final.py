#!/usr/bin/env python3
from pathlib import Path
import hashlib,json,shutil,subprocess
root=Path.cwd()
jar=root/'work/async-profiler-4.1/build/jar/jfr-converter.jar'
binaries={'go':root/'work/jfrconv-go-build/jfrconv','rust':root/'work/jfrconv-build-target/release/jfrconv'}
expected={'go':'144204d7e755411903b058f5153aa583bd4adf4c1212667c52d73ae75c85bca6','rust':'f21a6cc70ff55683d0ddc62a28e179c697e2547b97b91f615cb5620fea3376d0'}
def digest(p):return hashlib.sha256(p.read_bytes()).hexdigest()
old=json.loads((root/'work/ui-parity/report.json').read_text())
base=[{'case':c['case'],'format':c['format'],'flags':c['flags'],'source':root/c['input']} for c in old['cases']]
corpora={}
for impl,binary in binaries.items():
    assert digest(binary)==expected[impl]
    directory=root/f'work/{impl}-ui-final'
    directory.mkdir(parents=True,exist_ok=True)
    frozen=directory/'jfrconv-measured';shutil.copyfile(binary,frozen);frozen.chmod(0o755)
    corpora[impl]={'implementation':'native '+('Go' if impl=='go' else 'Rust'),impl+'_binary_sha256':digest(frozen),'java_jar_sha256':digest(jar),'cases':[]}
    binaries[impl]=frozen

def pair(case,source,flags,fmt='html'):
    for impl,binary in binaries.items():
        directory=root/f'work/{impl}-ui-final'/case;directory.mkdir(parents=True,exist_ok=True)
        files={};commands={}
        for kind in ['java',impl]:
            output=directory/(kind+'.html')
            command=(['java','-jar',str(jar)] if kind=='java' else [str(binary)])+flags+['-o',fmt,str(source),str(output)]
            subprocess.run(command,check=True,capture_output=True,text=True)
            files[kind]=f'{case}/{kind}.html';commands[kind]=command
        corpora[impl]['cases'].append({'case':case,'format':fmt,'flags':flags,'source':str(source),'source_sha256':digest(source),'files':files,'commands':commands})
    print('generated',case,flush=True)

for c in base:
    source=c['source']
    if source==root/'work/ui-parity/ap-cpu/java.html':source=root/'work/go-ui-final/ap-cpu/java.html'
    pair(c['case'],source,c['flags'],c['format'])
ap=root/'work/fixtures/ap41.jfr'
pair('ap-regex-alternation',ap,['--cpu','--include',r'.*(?:RecordingWorkload|Thread).*','--exclude',r'.*(?:park|sleep).*'])
pair('ap-regex-casefold',ap,['--cpu','--include',r'(?i).*recordingworkload.*'])
pair('ap-highlight-regex',ap,['--cpu','--highlight',r'RecordingWorkload|Thread'])
pair('cross-go-html-filter',root/'work/go-ui-final/ap-cpu/go.html',['--include',r'.*RecordingWorkload.*','--exclude',r'.*park.*'])
pair('cross-rust-html-regex',root/'work/rust-ui-final/ap-cpu/rust.html',['--include',r'(?i).*recordingworkload.*'])
pair('java-html-regex-quoted',root/'work/go-ui-final/ap-cpu/java.html',['--include',r'.*\QRecordingWorkload\E.*'])
for impl,corpus in corpora.items():
    directory=root/f'work/{impl}-ui-final';(directory/'manifest.json').write_text(json.dumps(corpus,indent=2)+'\n')
    for dpr in [1,2]:
        command=['node',str(root/f'outputs/jfrconv-{ "go" if impl=="go" else "rs" }/tools/ui_renderer_parity.js'),'--corpus',str(directory/'manifest.json'),'--output',str(directory/f'renderer-dpr{dpr}'),'--dpr',str(dpr),'--strict']
        result=subprocess.run(command,check=True,text=True,capture_output=True)
        (directory/f'renderer-dpr{dpr}.log').write_text(result.stdout+result.stderr)
        print(impl,'DPR',dpr,'PASS',flush=True)
