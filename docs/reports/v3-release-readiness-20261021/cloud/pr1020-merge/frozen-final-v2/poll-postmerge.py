from pathlib import Path
import concurrent.futures,datetime,json,subprocess,sys
root=Path(__file__).resolve().parent;sha='e30299fa59ea6669ea71024eabe209840f241f19';n=sys.argv[1];assert n.isdigit();out=root/('postmerge-'+n);out.mkdir()
def api(endpoint):
 r=subprocess.run(['gh','api','--method','GET',endpoint],capture_output=True,text=True,timeout=60);assert r.returncode==0,'read-only API error';return json.loads(r.stdout)
runs=api('repos/WuKongIM/WuKongIM/actions/runs?head_sha='+sha+'&per_page=100&page=1');assert runs['total_count']==len(runs['workflow_runs'])<100
(out/'runs.json').write_text(json.dumps(runs,indent=2)+'\n')
def jobs(run):
 assert run['head_sha']==sha and run['run_attempt']==1
 j=api('repos/WuKongIM/WuKongIM/actions/runs/'+str(run['id'])+'/jobs?per_page=100&page=1');assert j['total_count']==len(j['jobs'])<100
 (out/(str(run['id'])+'-jobs.json')).write_text(json.dumps(j,indent=2)+'\n')
 return {'id':run['id'],'name':run['name'],'event':run['event'],'status':run['status'],'conclusion':run['conclusion'],'jobs':[{'id':x['id'],'name':x['name'],'status':x['status'],'conclusion':x['conclusion'],'active_steps':[s['name'] for s in x.get('steps',[]) if s['status']=='in_progress'],'failed_steps':[s['name'] for s in x.get('steps',[]) if s['conclusion']=='failure']} for x in j['jobs']]}
s={'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'sha':sha,'runs':list(concurrent.futures.ThreadPoolExecutor(max_workers=2).map(jobs,runs['workflow_runs']))};(out/'summary.json').write_text(json.dumps(s,indent=2)+'\n');print(json.dumps(s))
