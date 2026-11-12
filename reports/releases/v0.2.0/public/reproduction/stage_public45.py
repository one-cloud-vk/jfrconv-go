#!/usr/bin/env python3
"""Stage pinned4.5 transport adapters while preserving every public assertion."""
from pathlib import Path
import argparse,hashlib,importlib.util,json,shutil,sys
root=Path.cwd()
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source',type=Path,default=root/'work/async-profiler-4.5')
parser.add_argument('--dest',type=Path,default=root/'work/go-v45/public-native')
parser.add_argument('--transport-helpers',type=Path,default=root/'outputs/jfrconv-go/tools/upstream_tests.py')
parser.add_argument('--bridge-template',type=Path,default=root/'outputs/jfrconv-go/tools/upstream/GoBridge.java')
parser.add_argument('--upstream-manifest',type=Path,default=root/'work/go-v45/upstream-source.json')
args=parser.parse_args();source=args.source.resolve();dest=args.dest.resolve()
if dest.exists():raise SystemExit('Refusing to replace native test staging')
spec=importlib.util.spec_from_file_location('old_transport_helpers',args.transport_helpers);helpers=importlib.util.module_from_spec(spec);spec.loader.exec_module(helpers)
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
pinned=json.loads(args.upstream_manifest.read_text())
assert pinned['commit']=='11aaea310cb75843f0e7532365d8cedc8d0e14c8'
for name,expected in pinned['files'].items():assert sha(source/name)==expected,name
original={str(p.relative_to(source)):sha(p) for p in (source/'test/test').rglob('*.java')}
shutil.copytree(source,dest)
bridge=args.bridge_template.read_text()
needle='    public static int execute(String... args) throws IOException {'
registry='''    private static final Map<one.jfr.JfrReader, String> inputs =
            Collections.synchronizedMap(new WeakHashMap<one.jfr.JfrReader, String>());
    public static void register(one.jfr.JfrReader reader, String input) { inputs.put(reader, input); }
    public static void dumpOtlp(one.jfr.JfrReader reader, Arguments args, OutputStream output) throws IOException {
        String input = inputs.get(reader);
        if (input == null) throw new IOException("Native OTLP test transport requires a filename-backed reader");
        Path temporary = Files.createTempFile("jfrconv-public-", ".otlp");
        try {
            convert(input, temporary.toString(), args, "otlp");
            Files.copy(temporary, output);
        } finally { Files.deleteIfExists(temporary); }
    }
'''
assert needle in bridge;bridge=bridge.replace(needle,registry+'\n'+needle)
(dest/'src/converter/one/convert/GoBridge.java').write_text(bridge)
changed=[]
def patch(relative,signature,body):
 p=dest/relative;p.write_text(helpers.body_replace(p.read_text(),signature,body));changed.append(relative)
patch('test/one/profiler/test/Output.java','public static Output convertJfrToCollapsed(', '        return new Output(new String(one.convert.GoBridge.collapsed(input, args), "UTF-8").split(System.lineSeparator()));')
for name,fmt in [('JfrToFlame','html'),('JfrToHeatmap','heatmap')]:
 expr='"collapsed".equals(args.output) ? "collapsed" : "html"' if fmt=='html' else '"heatmap"'
 patch(f'src/converter/one/convert/{name}.java','public static void convert(String input, String output, Arguments args)',f'        GoBridge.convert(input, output, args, {expr});')
patch('src/converter/one/convert/Main.java','public static void main(String[] argv)','        GoBridge.main(argv);')
p=dest/'src/converter/one/jfr/JfrReader.java';text=p.read_text();needle='    public JfrReader(String fileName) throws IOException {\n';assert needle in text;text=text.replace(needle,needle+'        one.convert.GoBridge.register(this, fileName);\n',1);p.write_text(text);changed.append('src/converter/one/jfr/JfrReader.java')
patch('src/converter/one/convert/JfrToOtlp.java','public void dump(OutputStream out)','        GoBridge.dumpOtlp(jfr, args, out);')
after={str(p.relative_to(dest)):sha(p) for p in (dest/'test/test').rglob('*.java')};assert original==after
manifest={'release':'v4.5','commit':'11aaea310cb75843f0e7532365d8cedc8d0e14c8','source':str(source),'staging':str(dest),'assertion_sources':original,'assertion_files_unchanged':True,'changed_transport_files':changed,'added_transport_file':'src/converter/one/convert/GoBridge.java','transport_scope':{'native_CLI':'Main.main, Output.convertJfrToCollapsed and static flame/heatmap entry points invoke the native executable with actual requested output paths including /dev/null.','native_OTLP_bytes':'Filename-backed JfrReader registration is a test-only weak map. JfrToOtlp.dump writes native CLI bytes; unchanged FromJfr protobuf assertions inspect these bytes. Original Java convert() can execute before dump.','latencyFilter':'Anonymous Java JfrConverter visitor assertion remains Java-side and is explicitly supplemented by mandatory native/Java collapsed parity on its exact recording and latency flags.'}}
(dest/'public-test-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');print(dest)
