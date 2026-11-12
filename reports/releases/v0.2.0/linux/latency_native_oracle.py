#!/usr/bin/env python3
"""Supplement the unchanged Java visitor test with actual native filtered output."""
from pathlib import Path
from collections import Counter
import argparse,hashlib,json,subprocess

def run(java,jar,recording,native,minimum_ms,output):
    output=Path(output);output.mkdir(parents=True,exist_ok=True)
    flags=['--wall','--latency',str(minimum_ms)+'ms'];maps={};commands={}
    for kind,base in [('java',[str(java),'-jar',str(jar)]),('native',[str(native)])]:
        target=output/(kind+'.collapsed');target.unlink(missing_ok=True)
        command=[*base,*flags,'-o','collapsed',str(recording),str(target)];commands[kind]=command
        completed=subprocess.run(command,capture_output=True,text=True)
        (output/(kind+'.stdout.log')).write_text(completed.stdout);(output/(kind+'.stderr.log')).write_text(completed.stderr)
        if completed.returncode:raise RuntimeError(f'{kind} latency oracle failed: {completed.stderr}')
        values=Counter()
        for line in target.read_text().splitlines():stack,count=line.rsplit(' ',1);values[stack]+=int(count)
        maps[kind]=values
    actual=maps['native'];conditions={'has_events':sum(actual.values())>0,'showcase1_present':any('test/jfrconverter/Tracer.showcase1' in s for s in actual),'showcase2_present':any('test/jfrconverter/Tracer.showcase2' in s for s in actual),'showcase3_absent':not any('test/jfrconverter/Tracer.showcase3' in s for s in actual)}
    def sha(path):return hashlib.sha256(Path(path).read_bytes()).hexdigest()
    report={'kind':'Native supplement to unchanged Java anonymous-visitor latencyFilter assertion','input':str(recording),'input_sha256':sha(recording),'native_binary_sha256':sha(native),'java_jar_sha256':sha(jar),'minimum_latency_ms':minimum_ms,'commands':commands,'same_full_stack_count_map':maps['java']==actual,'native_conditions':conditions,'java_total':sum(maps['java'].values()),'native_total':sum(actual.values()),'passed':maps['java']==actual and all(conditions.values())}
    (output/'report.json').write_text(json.dumps(report,indent=2)+'\n');return report

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--java',required=True);p.add_argument('--jar',required=True,type=Path);p.add_argument('--recording',required=True,type=Path);p.add_argument('--native',required=True,type=Path);p.add_argument('--minimum-ms',type=int,required=True);p.add_argument('--output',type=Path,required=True);a=p.parse_args();r=run(a.java,a.jar,a.recording,a.native,a.minimum_ms,a.output);print(json.dumps(r,indent=2));raise SystemExit(0 if r['passed'] else 1)
