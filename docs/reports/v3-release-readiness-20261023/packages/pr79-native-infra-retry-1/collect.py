import concurrent.futures,datetime,json,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parent
HEAD='b100f91a59902ad8d93b0632f9c84f3f7468e5f2'
RUN=39821491408
stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');out=ROOT/'snapshots'/stamp;out.mkdir(parents=True,exist_ok=False)
items=[('run.json',f'repos/WuKongIM/packages/actions/runs/{RUN}'),('attempt-2-run.json',f'repos/WuKongIM/packages/actions/runs/{RUN}/attempts/2'),('attempt-2-jobs.json',f'repos/WuKongIM/packages/actions/runs/{RUN}/attempts/2/jobs?per_page=100'),('all-jobs.json',f'repos/WuKongIM/packages/actions/runs/{RUN}/jobs?filter=all&per_page=100'),('artifacts.json',f'repos/WuKongIM/packages/actions/runs/{RUN}/artifacts?per_page=100'),('checks.json',f'repos/WuKongIM/packages/commits/{HEAD}/check-runs?per_page=100'),('main-ref.json','repos/WuKongIM/packages/git/ref/heads/main'),('pr.json','repos/WuKongIM/packages/pulls/79')]
def get(item):
 n,e=item;p=subprocess.run(['gh','api',e],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=90)
 (out/n).write_bytes(p.stdout)
 if p.returncode:(out/(n+'.stderr')).write_bytes(p.stderr);raise RuntimeError(n+': '+p.stderr.decode())
 return n,json.loads(p.stdout)
with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:results=dict(pool.map(get,items))
r=results['attempt-2-run.json'];js=results['attempt-2-jobs.json']['jobs'];assert r['head_sha']==HEAD and r['run_attempt']==2
summary={'observed_at_utc':stamp,'head':HEAD,'run_id':RUN,'run_attempt':r['run_attempt'],'status':r['status'],'conclusion':r['conclusion'],'main_sha':results['main-ref.json']['object']['sha'],'attempt_jobs':[{'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'current_steps':[s['name'] for s in j['steps'] if s['status']=='in_progress']} for j in js],'all_checks':[{'name':c['name'],'id':c['id'],'status':c['status'],'conclusion':c['conclusion']} for c in results['checks.json']['check_runs']]}
(out/'summary.json').write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps(summary,indent=2))
