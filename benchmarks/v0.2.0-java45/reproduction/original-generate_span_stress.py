#!/usr/bin/env python3
"""Deterministic4.5-layout stress fixture; separate from real official-API JFRs."""
from pathlib import Path
import hashlib,json,sys
ROOT=Path(__file__).resolve().parents[3]
sys.path.insert(0,str(ROOT/'outputs/jfrconv-go/tools'))
from fixtures import EXEC,SPAN,TICKS,record,values,write_chunk
p=Path(__file__).with_name('span-stress45.jfr')
with p.open('wb') as output:
 for chunk in range(4):
  base=TICKS*(chunk+1)
  events=[record(EXEC,values(base+j*2000,1+j%2,1+j%3,0)) for j in range(500000)]
  #20us intervals separated by20us gaps; events exactly at end must be excluded.
  events.extend(record(SPAN,values(base+j*40000,20000,1+(j//2)%2,1+j%2)) for j in range(25000))
  if chunk:events.append(record(SPAN,values(base-100000,200000,1,1)))
  write_chunk(output,events,label='stress'+str(chunk),chunk_index=chunk,span_current=True,tag_strings=['selected','other'])
manifest={'kind':'deterministic synthetic stress using exact4.5 Span constant-pool layout','producer':'generate_span_stress.py','producer_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'path':str(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'bytes':p.stat().st_size,'chunks':4,'CPU_events':2000000,'Span_events':100003,'threads':2,'tags':['selected','other'],'properties':['monotonic distinct CPU timestamps across each1s chunk','20us intervals with20us gaps and exact end-boundary samples','per-thread/tag selection','three selected spans crossing real header/CP boundaries']}
p.with_suffix('.json').write_text(json.dumps(manifest,indent=2)+'\n');print(json.dumps(manifest,indent=2))
