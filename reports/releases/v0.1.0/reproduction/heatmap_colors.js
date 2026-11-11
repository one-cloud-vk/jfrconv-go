#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
// Decode only trusted bundled 4.1 JavaScript plus input data. Input scripts are
// never executed. Compare USED semantic method identities, numeric IDs and RGB.
'use strict';
const fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto');
const template=fs.readFileSync(path.join(__dirname,'../assets/heatmap.html'),'utf8');
function section(a,b){const from=template.indexOf(a),to=template.indexOf(b,from);if(from<0||to<0)throw Error('Missing upstream decoder');return template.slice(from,to);}
const decoder=Function('const zoomToGroupSize=[1,50,250];'+section('class DataBuffer {','class HeatmapCollection extends')+section('function calculateMinMax(','function calculateInitialZoom(')+section('const palette =','function samples(')+'return {DataBuffer,decodeExecutions,palette,getColorStable};')();
function colors(file){
 const html=fs.readFileSync(file,'utf8');
 if(html.trim()==='No samples found')return [];
 function data(id){const at=html.indexOf('<pre id="'+id+'">'),from=html.indexOf('>',at)+1,to=html.indexOf('</pre>',from);if(at<0||to<0)throw Error('Missing heatmap data');return html.slice(from,to);}
 const match=html.match(/let cpool = \[([\s\S]*?)\];/);if(!match)throw Error('Missing cpool');
 const strings=JSON.parse('['+match[1].replace(/,\s*$/,'')+']');
 const starts=new Set(),initial=new decoder.DataBuffer(data('executionsHeatmap'));
 for(let i=0,n=initial.nextVarInt();i<n;i++)starts.add(initial.nextVarInt());
 const methods=[null],input=new decoder.DataBuffer(data('methods'));input.nextInt6();
 for(let i=0,n=input.nextVarInt();i<n;i++){
  const cls=input.nextVarInt(),name=input.nextVarInt(),bci=input.nextInt18(),line=input.nextInt18(),kind=input.nextInt6(),id=i+1;
  methods.push({key:[strings[cls],strings[name],bci,line,kind,starts.has(id)],id,rgb:decoder.getColorStable(decoder.palette[kind],id)});
 }
 const decoded=decoder.decodeExecutions(new decoder.DataBuffer(data('executionsHeatmap'))),used=new Set();
 for(const position of new Set(decoded.chunksToStorage))for(let i=0;i<decoded.storageSizes[position];i++)used.add(decoded.storage[position+i]);
 return Array.from(used,id=>{if(!methods[id])throw Error('Invalid method reference');return methods[id];}).sort((a,b)=>{const x=JSON.stringify(a.key),y=JSON.stringify(b.key);return x<y?-1:x>y?1:a.id-b.id;});
}
function digest(rows){return crypto.createHash('sha256').update(JSON.stringify(rows)).digest('hex');}
module.exports=colors;
if(require.main===module){
 try{
  if(process.argv.length!==4)throw Error('Usage: node heatmap_colors.js JAVA.html NATIVE.html');
  const java=colors(process.argv[2]),native=colors(process.argv[3]),expected=new Map(java.map(row=>[JSON.stringify(row.key),row])),actual=new Map(native.map(row=>[JSON.stringify(row.key),row]));
  const differences=[];
  for(const [key,row] of expected){const found=actual.get(key);if(!found||found.id!==row.id||found.rgb!==row.rgb)differences.push({identity:row.key,java:row,native:found||null});}
  for(const [key,row] of actual)if(!expected.has(key))differences.push({identity:row.key,java:null,native:row});
  const result={same_used_method_IDs_RGB:differences.length===0,java_methods:java.length,native_methods:native.length,java_digest:digest(java),native_digest:digest(native),difference_count:differences.length,differences:differences.slice(0,20)};
  console.log(JSON.stringify(result,null,2));if(differences.length)process.exitCode=1;
 }catch(error){console.error(error.stack||error);process.exitCode=2;}
}
