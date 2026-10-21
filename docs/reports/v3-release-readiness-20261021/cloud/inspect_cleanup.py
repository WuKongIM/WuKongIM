#!/usr/bin/env python3
"""Read only allowlisted non-secret cleanup JSON members from small retained artifacts."""
import concurrent.futures,hashlib,io,json,pathlib,re,subprocess,zipfile
OUT=pathlib.Path(__file__).resolve().parent
arts=json.loads((OUT/'artifact-inventory.json').read_text())['artifacts']
latest={}
for a in arts:
 if not a['expired'] and a['name'].startswith('chat-lifecycle-rehearsal-cleanup-'):
  latest.setdefault(a['name'],a)
allowed={'cleanup.json','zero-inventory.json','release-selector.json','manifest.json'}
def run(a):
 if a['size_in_bytes']>200000:raise RuntimeError('oversized cleanup '+a['name'])
 p=subprocess.run(['gh','api','--method','GET',f"repos/WuKongIM/WuKongIM/actions/artifacts/{a['id']}/zip"],capture_output=True,timeout=90)
 if p.returncode:raise RuntimeError('artifact download failed '+str(a['id']))
 digest=hashlib.sha256(p.stdout).hexdigest()
 if a.get('digest') and a['digest']!='sha256:'+digest:raise RuntimeError('artifact digest mismatch')
 result={'artifact_id':a['id'],'artifact_name':a['name'],'producer_run':a['workflow_run'],'created_at':a['created_at'],'zip_sha256':digest,'byte_size':len(p.stdout),'members':[],'allowlisted_json':{}}
 with zipfile.ZipFile(io.BytesIO(p.stdout)) as z:
  for i in z.infolist():
   result['members'].append({'name':i.filename,'size':i.file_size})
   if i.filename in allowed:
    if i.file_size>100000:raise RuntimeError('oversized json')
    result['allowlisted_json'][i.filename]=json.loads(z.read(i))
 dest=OUT/'cleanup-selected-json';dest.mkdir(exist_ok=True)
 with (dest/(str(a['id'])+'.json')).open('x') as f:json.dump(result,f,indent=2);f.write('\n')
 return {'id':a['id'],'name':a['name'],'json_members':list(result['allowlisted_json']), 'cleanup':result['allowlisted_json'].get('cleanup.json')}
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:r=list(pool.map(run,latest.values()))
with (OUT/'cleanup-inspection-index.json').open('x') as f:json.dump(r,f,indent=2);f.write('\n')
print(json.dumps({'artifacts':len(r),'with_zero_inventory_flag':sum(x.get('cleanup',{}).get('zero_inventory') is True for x in r),'missing_expected_cleanup_member':[x['id'] for x in r if x['cleanup'] is None]},indent=2))
