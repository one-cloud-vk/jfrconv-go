#!/usr/bin/env python3
"""Final immutable-binary official4.5 Span/Unicode/trace comparison."""
from pathlib import Path
import hashlib,json,subprocess,sys
root=Path(__file__).resolve().parents[2]
tools=root/'outputs/jfrconv-go/tools';sys.path.insert(0,str(tools))
from validate import collapsed,semantic_pprof,protobuf,integers
from otlp45 import semantic_otlp
work=root/'work/go-v45';out=work/'final-official-oracles'
assert not out.exists();out.mkdir()
binary=work/'candidate/jfrconv';java=Path('/opt/homebrew/opt/openjdk@21/bin/java');jar=root/'work/async-profiler-4.5/build/jar/jfr-converter.jar'
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
assert sha(binary)=='a9d7e8fc1fb440157df5f56ae34029e78cdf5b79005bc38d7a9406da4df1feb5'
assert sha(jar)=='ded43c350f75766b82570ac297c9dd3c3efd69e73de26474615cf2df90e1fc94'
assert sha(work/'source-freeze.json')=='680a235d0231be4c6c001aea6b633ece193b19893f7700f89128e7b627147897'
commands=[]
def run(cmd):
 cmd=list(map(str,cmd));r=subprocess.run(cmd,capture_output=True,text=True,timeout=120)
 commands.append({'command':cmd,'exit_code':r.returncode})
 assert r.returncode==0,(cmd,r.stdout,r.stderr)
 return r.stdout

def canon(path,fmt):
 if fmt=='collapsed':return sorted(collapsed(path).items())
 if fmt in ['pprof','pb.gz']:
  import gzip
  raw=path.read_bytes();raw=gzip.decompress(raw)if raw[:2]==b'\x1f\x8b'else raw
  fields=protobuf(raw);strings=[v.decode('utf8')for n,w,v in fields if n==6]
  types=[]
  for n,w,v in fields:
   if n==1:
    f=protobuf(v);types.append(tuple(strings[(integers(f,k)or[0])[0]]for k in [1,2]))
  return {'samples':sorted(semantic_pprof(path).items()),'sample_types':types,'time':integers(fields,9),'duration':integers(fields,10)}
 if fmt=='otlp':return semantic_otlp(path)
 if fmt=='heatmap':return json.loads(run(['node',tools/'heatmap_decode.js',path]))
 if fmt=='html':
  back=path.with_suffix(path.suffix+'.decoded.collapsed')
  run([java,'-jar',jar,'-o','collapsed',path,back]);return sorted(collapsed(back).items())
 raise ValueError(fmt)

def compare(name,fmt,left,right,flags,input):
 a=canon(left,fmt);b=canon(right,fmt)
 assert a==b,(name,fmt,a,b)
 color=None
 if fmt=='heatmap':
  color=json.loads(run(['node',tools/'heatmap_colors.js',left,right]));assert color['same_used_method_IDs_RGB']
 print('PASS',name,fmt,flush=True)
 return {'case':name,'format':fmt,'flags':flags,'input':str(input),'input_sha256':sha(input),'java_output_sha256':sha(left),'native_output_sha256':sha(right),'canonical_equal':True,'canonical_sha256':hashlib.sha256(json.dumps(a,sort_keys=True,ensure_ascii=False).encode()).hexdigest(),**({'colors':color}if color else{})}

manifest=json.loads((work/'fixtures/java45-oracles/manifest.json').read_text());recording=Path(manifest['recording']);assert sha(recording)==manifest['recording_sha256']
rows=[]
for c in manifest['cases']:
 ref=Path(c['file']);assert sha(ref)==c['sha256']
 target=out/('span-'+c['case']+'.'+c['format'])
 run([binary,*c['flags'],'-o',c['format'],recording,target])
 rows.append(compare('span-'+c['case'],c['format'],ref,target,c['flags'],recording))
assert len(rows)==21
unicode=work/'fixtures/span-unicode45.jfr';assert sha(unicode)=='d2426da91d244876f75ffec90bf1540f0a6f6b2340de6c75a948668a3bd93523'
for mode in [[],['--cpu']]:
 for i,tag in enumerate(['unicode-α/雪','emoji-��','nul-��-X']):
  flags=[*mode,'--tag',tag]
  for fmt in ['collapsed','html','pprof','pb.gz','otlp','heatmap']:
   name=f'unicode-{("cpu"if mode else "default")}-{i}'
   files={}
   for kind,cmd in [('java',[java,'-jar',jar]),('native',[binary])]:
    target=out/(name+'.'+kind+'.'+fmt);run([*cmd,*flags,'-o',fmt,unicode,target]);files[kind]=target
   rows.append(compare(name,fmt,files['java'],files['native'],flags,unicode))
trace=json.loads((work/'reference/methodtrace-oracle/report.json').read_text());recording=root/'outputs/jfrconv-go/tests/fixtures/method-trace.jfr';assert sha(recording)==trace['input_sha256']
for c in trace['rows']:
 ref=out/(c['case']+'.java.collapsed');ref.write_text(c['collapsed'])
 target=out/(c['case']+'.native.collapsed');run([binary,*c['flags'],'-o','collapsed',recording,target])
 rows.append(compare(c['case'],'collapsed',ref,target,c['flags'],recording))
assert len(rows)==60
report={'release':'v0.2.0','target':'async-profiler v4.5','upstream_commit':manifest['commit'],'native_binary_sha256':sha(binary),'java_jar_sha256':sha(jar),'source_freeze_sha256':sha(work/'source-freeze.json'),'all_passed':True,'official_span_cases':21,'unicode_tag_cases':6,'unicode_formats_per_case':6,'unicode_comparisons':36,'method_trace_cases':3,'total_comparisons':len(rows),'driver_sha256':sha(Path(__file__)),'note':'Official21pairs use hash-verified immutableJava4.5 oracle outputs. All36 Unicode pairs generated freshly with exactJava4.5 and finalnativebinary; three trace goldens are pinned pristineJava4.5. HTML compared by originalJava reader, heatmaps by original4.5 decoder plus used methodID/RGB.','cases':rows,'commands':commands}
(out/'report.json').write_text(json.dumps(report,indent=2,ensure_ascii=False)+'\n')
print('PASS all60 officialSpan/Unicode/trace comparisons',flush=True)
