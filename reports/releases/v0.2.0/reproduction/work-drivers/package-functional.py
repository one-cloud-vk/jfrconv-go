#!/usr/bin/env python3
"""Copy compact final0.2.0 functional evidence without running workloads."""
from pathlib import Path
import hashlib,json,re,shutil
root=Path(__file__).resolve().parents[2]
project=root/'outputs/jfrconv-go'
work=root/'work/go-v45'
target=project/'reports/releases/v0.2.0'
target.mkdir(parents=True,exist_ok=True)
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
copies={}
def copy(source,name):
 source=Path(source);source=source if source.is_absolute()else root/source
 dest=target/name;dest.parent.mkdir(parents=True,exist_ok=True)
 shutil.copyfile(source,dest)
 assert sha(source)==sha(dest)
 copies[name]={'source':str(source.relative_to(root)),'sha256':sha(dest),'bytes':dest.stat().st_size}
for name in ['source-freeze.json','build-source-freeze.json','helper-amendment.json','upstream-source.json']:
 copy(work/name,name)
copy(work/'reference/build-manifest.json','reference/build-manifest.json')
copy(work/'reference/protobuf-runtime.json','reference/protobuf-runtime.json')
for source in (work/'checks').iterdir():
 if source.is_file():copy(source,'macos/'+source.name)
sem=work/'final-semantic-jdk21-attempt2'
copy(sem/'validation.json','validation.json');copy(sem/'validation.log','validation.log')
copy(work/'final-semantic-jdk21/validation.json','attempts/missing-wall-setting/validation.json')
copy(work/'final-semantic-jdk21/validation.log','attempts/missing-wall-setting/validation.log')
copy(work/'final-ui-driver.log','attempts/renderer-missing-dom/driver.log')
ui=work/'final-ui-jdk21'
for source,name in [('manifest.json','manifest.json'),('static-shell.json','static-shell.json'),('heatmap-colors.json','heatmap-colors.json'),('renderer45-dpr1/report.json','renderer-dpr1.json'),('renderer45-dpr2/report.json','renderer-dpr2.json'),('renderer45-dpr1.log','renderer-dpr1.log'),('renderer45-dpr2.log','renderer-dpr2.log')]:
 copy(ui/source,'ui/'+name)
copy(work/'renderer-host-amendment.json','ui/renderer-host-amendment.json')
copy(work/'final-heatmap-driver.log','ui/heatmap-driver.log')
for name in ['manifest.json','spans.json','java-utf8-goldens.json']:
 copy(work/'fixtures'/name,'official-span/'+name)
copy(work/'fixtures/java45-oracles/manifest.json','official-span/java45-oracles.json')
copy(work/'reference/unicode-fixed2/report.json','official-span/unicode.json')
copy(work/'reference/methodtrace-oracle/report.json','official-span/methodtrace-reference.json')
for name in ['validate.py','fixtures.py','otlp45.py','protobuf_wire.py','heatmap_decode.js','heatmap_colors.js','ui_renderer_parity.js']:
 copy(project/'tools'/name,'reproduction/tools/'+name)
for name in ['verify-final-ui.py','verify-final-heatmap45.py','adapt-renderer45.py','ui-renderer-before45.js','validate-before45.py','package-functional.py']:
 copy(work/name,'reproduction/work-drivers/'+name)
freeze=json.loads((work/'source-freeze.json').read_text())
assert sha(work/'source-freeze.json')=='680a235d0231be4c6c001aea6b633ece193b19893f7700f89128e7b627147897'
assert all(sha(project/name)==expected for name,expected in freeze['files'].items())
assert sha(work/'candidate/jfrconv')==freeze['native_binary_sha256']
checks=json.loads((work/'checks/checks.json').read_text())
assert all(c['exit_code']==0 for c in checks['checks'])
testlog=(work/'checks/tests.log').read_text()
passes=len(re.findall(r'^--- PASS:',testlog,re.M));skips=len(re.findall(r'^--- SKIP:',testlog,re.M))
assert passes==86 and skips==0,(passes,skips)
semantic=json.loads((sem/'validation.json').read_text());assert semantic['count']==81 and not semantic['failures'] and not semantic['pending']
heat=json.loads((ui/'heatmap-colors.json').read_text());assert heat['all_exact'] and heat['exact_comparisons']==22
render=[]
for dpr in [1,2]:
 report=json.loads((ui/f'renderer45-dpr{dpr}/report.json').read_text())
 rows=[c for c in report['cases']if c['format']=='html']
 counts={'dpr':dpr,'flamegraph_pairs':len(rows),'phases':sum(len(c['phases'])for c in rows),'assertions':sum(2*c['checks_per_implementation']for c in rows),'all_phases_identical':all(c['all_phases_identical']for c in rows)}
 assert counts['phases']==357 and counts['assertions']==882 and counts['all_phases_identical']
 render.append(counts)
evidence={'release':'v0.2.0','compatibility_target':'async-profiler v4.5','upstream_commit':freeze['upstream_commit'],'native_binary_sha256':freeze['native_binary_sha256'],'java_jar_sha256':freeze['java_jar_sha256'],'source_freeze_sha256':sha(work/'source-freeze.json'),'build_source_freeze_sha256':sha(work/'build-source-freeze.json'),'source_files':len(freeze['files']),'source_files_match_delivery':True,'native_source_unchanged_after_build':True,'helper_only_amendment':'helper-amendment.json','macos':{'native_tests_passed':passes,'native_tests_skipped':skips,'format_tests_vet_build_python_codecs':True,'python_codec_tests_passed':6,'report':'macos/checks.json'},'semantic':{'passed':81,'failures':0,'pending':0,'real_recordings':3,'report':'validation.json'},'ui':{'page_pairs':24,'flamegraph_pairs':21,'static_shells_identical':21,'renderer':render,'heatmap_exact_comparisons':22,'heatmap_base_modes':10,'heatmap_thread_order_span_cases':9,'heatmap_weighted_wall_first_time_cases':3,'report':'ui/heatmap-colors.json','scope':'Original4.5 JavaScript, deterministic RNG and DOM/canvas mocks; not a real-browser pixel/layout test. Heatmap corpus entries are seeded only; exact heatmap IDs/RGB/timelines use separate decoders.'},'unicode':{'same_final_binary':True,'real_jni_cases_passed':6,'java_utf8_vectors':16,'report':'official-span/unicode.json'},'attempts':{'initial_semantic':'attempts/missing-wall-setting/validation.json','initial_ui_host':'attempts/renderer-missing-dom/driver.log','explanation':'Initial pristineJava4.5 wall heatmap failed before comparison because legacy synthetic fixture omitted ActiveSetting. Final validator uses shipped valid4.5 weighted wall fixture. Original renderer host lacked new4.5 DOM controls; only host APIs changed, all original assertions retained and four newcontrol assertions added. No native production/test source changed.'},'reports_note':'Compact evidence only: generated HTML, seeded pages, binaries and recording duplicates are intentionally not copied. Original execution paths/commands are retained in reports. Work-driver snapshots record actual staged execution; shipped tools accept ordinary user-supplied inputs. Public/Linux/performance evidence is packaged separately.'}
(target/'evidence.json').write_text(json.dumps(evidence,indent=2)+'\n')
(target/'functional-copy-manifest.json').write_text(json.dumps({'release':'v0.2.0','copies':copies},indent=2)+'\n')
print(json.dumps({'path':str(target),'copied_files':len(copies),'bytes':sum(x['bytes']for x in copies.values()),'native_tests':passes,'semantic':81,'renderer':render,'heatmap':22},indent=2))
