#!/usr/bin/env python3
"""Bounded GET-only inventory of GitHub metadata; no artifact payload or cloud call."""
import concurrent.futures,datetime,json,pathlib,subprocess
OUT=pathlib.Path(__file__).resolve().parent
PREFIX='repos/WuKongIM/WuKongIM/'
def get(path):
 p=subprocess.run(['gh','api','--method','GET',PREFIX+path,'-H','X-GitHub-Api-Version: 2026-03-10'],capture_output=True,text=True,timeout=60)
 if p.returncode: raise RuntimeError(path+': '+p.stderr)
 return json.loads(p.stdout)
def save(name,data):
 with (OUT/name).open('x') as f:json.dump(data,f,indent=2);f.write('\n')
all_artifacts=[];pages=[]
for page in range(1,201):
 data=get(f'actions/artifacts?per_page=100&page={page}')
 items=[{k:a.get(k) for k in ['id','name','size_in_bytes','created_at','updated_at','expires_at','expired','digest','workflow_run']} for a in data['artifacts']]
 pages.append({'page':page,'total_count':data['total_count'],'count':len(items)})
 all_artifacts.extend(items)
 if len(items)<100:break
else:raise RuntimeError('metadata inventory incomplete at 200 pages')
save('artifact-inventory.json',{'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'complete_paginated_snapshot':True,'pages':pages,'artifacts':all_artifacts,'limitation':'GitHub artifacts only; deleted/expired historical artifacts and cloud inventory are not proved absent.'})
fields=['id','run_attempt','name','path','event','status','conclusion','head_sha','head_branch','created_at','updated_at','html_url']
active=[]
for status in ['queued','in_progress','waiting','requested','pending']:
 pages_for_status=[];runs=[]
 for page in range(1,21):
  data=get(f'actions/runs?status={status}&per_page=100&page={page}')
  pages_for_status.append({'page':page,'total_count':data['total_count'],'count':len(data['workflow_runs'])})
  runs.extend({k:r.get(k) for k in fields} for r in data['workflow_runs'])
  if len(data['workflow_runs'])<100:break
 else:raise RuntimeError('active run inventory incomplete')
 active.append({'status_filter':status,'pages':pages_for_status,'runs':runs})
save('nonterminal-runs.json',{'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'filters':active})
for runid in ['32570381582','35720371242','33502821176','33502076809','33479692602','32573560442']:
 d=get(f'actions/runs/{runid}/jobs?per_page=100')
 save('jobs-'+runid+'.json',{'run_id':runid,'total_count':d['total_count'],'jobs':[{k:j.get(k) for k in ['id','name','status','conclusion','started_at','completed_at','steps']} for j in d['jobs']]})
print(json.dumps({'artifact_count':len(all_artifacts),'relevant_artifact_count':sum(a['name'].startswith(('chat-lifecycle-','cloud-lease-')) for a in all_artifacts),'active_run_counts':{a['status_filter']:len(a['runs']) for a in active}},indent=2))
