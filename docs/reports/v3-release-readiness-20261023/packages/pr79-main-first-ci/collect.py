import concurrent.futures,datetime,json,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parent
HEAD='721712e6d77c5f159fff94dcdb2bd1fb84ad464c';runs=(39825002244,39825002051)
stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');out=ROOT/'snapshots'/stamp;out.mkdir(parents=True,exist_ok=False)
items=[('runs.json',f'repos/WuKongIM/packages/actions/runs?head_sha={HEAD}&per_page=100'),('checks.json',f'repos/WuKongIM/packages/commits/{HEAD}/check-runs?per_page=100'),('main-commit.json',f'repos/WuKongIM/packages/git/commits/{HEAD}'),('pr.json','repos/WuKongIM/packages/pulls/79')]
for rid in runs:
 for suffix,endpoint in [('run',f'actions/runs/{rid}'),('jobs',f'actions/runs/{rid}/jobs?filter=all&per_page=100'),('artifacts',f'actions/runs/{rid}/artifacts?per_page=100')]:items.append((str(rid)+'-'+suffix+'.json','repos/WuKongIM/packages/'+endpoint))
def get(item):
 n,e=item;p=subprocess.run(['gh','api',e],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=90)
 (out/n).write_bytes(p.stdout)
 if p.returncode:(out/(n+'.stderr')).write_bytes(p.stderr);raise RuntimeError(n+': '+p.stderr.decode())
 return n,json.loads(p.stdout)
with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:d=dict(pool.map(get,items))
s={'observed_at_utc':stamp,'head':HEAD,'runs':[],'checks':[]}
for rid in runs:
 run=d[str(rid)+'-run.json'];assert run['head_sha']==HEAD and run['run_attempt']==1 and run['event']=='push'
 s['runs'].append({'id':rid,'status':run['status'],'conclusion':run['conclusion'],'attempt':run['run_attempt'],'event':run['event'],'jobs':[{'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'current_steps':[x['name'] for x in j['steps'] if x['status']=='in_progress']} for j in d[str(rid)+'-jobs.json']['jobs']]})
s['checks']=[{k:c[k] for k in ('id','name','status','conclusion','started_at','completed_at')} for c in d['checks.json']['check_runs']]
(out/'summary.json').write_text(json.dumps(s,indent=2)+'\n');print(json.dumps(s,indent=2))
