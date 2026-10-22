import concurrent.futures, datetime, json, pathlib, subprocess, sys
ROOT=pathlib.Path(__file__).resolve().parent
REPO='WuKongIM/packages'
HEAD='b100f91a59902ad8d93b0632f9c84f3f7468e5f2'
run_ids=(39821491408,39821491416,39821491479)
def get(endpoint):
 p=subprocess.run(['gh','api',endpoint],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=90)
 if p.returncode: raise RuntimeError(endpoint+': '+p.stderr.decode())
 return p.stdout
def capture(item):
 name, endpoint=item
 raw=get(endpoint)
 (out/name).write_bytes(raw)
 return name,json.loads(raw)
stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
out=ROOT/'snapshots'/stamp
out.mkdir(parents=True,exist_ok=False)
items=[('pr.json','repos/'+REPO+'/pulls/79'),('checks.json','repos/'+REPO+'/commits/'+HEAD+'/check-runs?per_page=100'),('runs.json','repos/'+REPO+'/actions/runs?head_sha='+HEAD+'&per_page=100')]
for r in run_ids:
 items.extend([(str(r)+'-run.json','repos/'+REPO+'/actions/runs/'+str(r)),(str(r)+'-jobs.json','repos/'+REPO+'/actions/runs/'+str(r)+'/jobs?filter=all&per_page=100'),(str(r)+'-artifacts.json','repos/'+REPO+'/actions/runs/'+str(r)+'/artifacts?per_page=100')])
with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
 results=dict(pool.map(capture,items))
summary={'observed_at_utc':stamp,'head':HEAD,'runs':[],'checks':[]}
for r in run_ids:
 d=results[str(r)+'-run.json']; js=results[str(r)+'-jobs.json']['jobs']
 summary['runs'].append({'id':r,'status':d['status'],'conclusion':d['conclusion'],'attempt':d['run_attempt'],'head_sha':d['head_sha'],'event':d['event'],'jobs':[{'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'current_steps':[s['name'] for s in j['steps'] if s['status']=='in_progress']} for j in js]})
summary['checks']=[{k:c[k] for k in ('id','name','status','conclusion','started_at','completed_at')} for c in results['checks.json']['check_runs']]
(out/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps(summary,indent=2))
