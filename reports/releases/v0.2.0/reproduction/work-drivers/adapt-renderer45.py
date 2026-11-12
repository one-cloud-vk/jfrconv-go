from pathlib import Path
import hashlib,json
root=Path(__file__).resolve().parents[2]
source=root/'outputs/jfrconv-go/tools/ui_renderer_parity.js'
target=root/'work/go-v45/ui-renderer45.js'
s=source.read_text()
changes=[]
def replace(old,new,label):
 global s
 assert s.count(old)==1,(label,s.count(old))
 s=s.replace(old,new)
 changes.append(label)
replace("'match','matchval']", "'match','matchval','darkmode','info','legend']", 'new4.5DOMelements')
replace("const elements={},draws=[],prompts=[],selection=[],scrolls=[];", "const elements={},draws=[],prompts=[],selection=[],scrolls=[],classes=new Set(),stored=new Map();", 'ownedThemeState')
replace("document:{body:{style:{font:'12px Verdana, sans-serif'}},getElementById", "localStorage:{getItem(key){return stored.get(key)||null;},setItem(key,value){stored.set(key,String(value));}},matchMedia(){return {matches:false};},getComputedStyle(){return {getPropertyValue(key){if(key!=='--bg')throw Error(key);return classes.has('dark')?'#1e1e1e':'#ffffff';}};},\n    document:{documentElement:{classList:{add(key){classes.add(key);},toggle(key){if(classes.has(key)){classes.delete(key);return false;}classes.add(key);return true;}}},body:{style:{font:'12px Verdana, sans-serif'}},getElementById", 'themeDOMAPIs')
replace("vm.runInContext(source,context,{timeout:3000,filename:file});", "for(const match of html.matchAll(/<script(?:\\s[^>]*)?>([\\s\\S]*?)<\\/script>/gi)){if(match[1]===source)break;vm.runInContext(match[1],context,{timeout:3000,filename:file+':theme'});}\n  vm.runInContext(source,context,{timeout:3000,filename:file});", 'executeOriginalThemePrelude')
replace("const state={label,canvasHeight:height,", "const state={label,dark:classes.has('dark'),storedTheme:stored.get('flame-theme')||null,canvasHeight:height,", 'snapshotThemeState')
replace("const byLabel=Object.fromEntries(traces.map(t=>[t.label,t]));", "exec(\"document.getElementById('darkmode').onclick()\");traces.push(host.snapshot('darkmode-button'));\n  exec(\"window.onkeydown({key:'d'})\");traces.push(host.snapshot('darkmode-key'));\n  exec(\"document.getElementById('info').onmouseover()\");traces.push(host.snapshot('legend-show'));\n  exec(\"document.getElementById('info').onmouseout()\");traces.push(host.snapshot('legend-hide'));\n  const byLabel=Object.fromEntries(traces.map(t=>[t.label,t]));", 'fourAdditional4.5ControlPhases')
replace("const checks={", "const checks={\n    darkmode_button_changes_theme_and_persists:byLabel['darkmode-button'].dark&&!byLabel.initial.dark&&byLabel['darkmode-button'].storedTheme==='dark'&&byLabel['darkmode-button'].draws.some(d=>d[0]==='fillRect'&&d[1]==='#1e1e1e'),\n    darkmode_keyboard_restores_theme:!byLabel['darkmode-key'].dark&&byLabel['darkmode-key'].storedTheme==='light'&&byLabel['darkmode-key'].draws.some(d=>d[0]==='fillRect'&&d[1]==='#ffffff'),\n    legend_hover_shows_original_content:byLabel['legend-show'].dom.legend.style.display==='block'&&byLabel['legend-show'].dom.legend.style.left==='34px'&&byLabel['legend-show'].dom.legend.style.top==='74px',\n    legend_mouseout_hides_content:byLabel['legend-hide'].dom.legend.style.display==='none',", 'fourAdditional4.5AssertionsKeepOriginal17')
replace('Actual generated4.1 flamegraph', 'Actual generated4.5 flamegraph', 'correctReferenceDescription')
target.write_text(s)
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
report={'purpose':'Work-only4.5DOMhost adaptation; original17 interaction assertions unchanged, four new4.5 assertions added. Generated upstream scripts unchanged.','source':str(source),'source_sha256':sha(source),'adapted':str(target),'adapted_sha256':sha(target),'changes':changes}
(root/'work/go-v45/renderer-host-amendment.json').write_text(json.dumps(report,indent=2)+'\n')
