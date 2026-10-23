#!/usr/bin/env python3
import subprocess,json,pathlib,datetime,hashlib,concurrent.futures
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1030-ci-generation-1')
HEAD='56c287493a5e9fc3df65ea0631b99612672cd678'; BASE='27fa6d63cf3b2b6c3a9a28575f917d60cc054ded'
repo='repos/WuKongIM/WuKongIM/'
def get(p):
 r=subprocess.run(['gh','api','--method','GET',repo+p],capture_output=True,timeout=50)
 if r.returncode:raise RuntimeError((p,r.returncode,r.stderr[:300].decode()))
 assert 0<len(r.stdout)<16*1024*1024
 return json.loads(r.stdout)
def put(p,x):p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(x,indent=2)+'\n')
now=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');P=R/'polls'/now;P.mkdir(parents=True)
pr=get('pulls/1030');assert pr['head']['sha']==HEAD and pr['base']['sha']==BASE
put(P/'pr.json',pr)
runs=get('actions/runs?head_sha='+HEAD+'&per_page=100');assert runs['total_count']==len(runs['workflow_runs']);put(P/'head-runs.json',runs)
ids=[r['id'] for r in runs['workflow_runs'] if r['event']=='pull_request']
def collect(i):
 run=get('actions/runs/'+str(i));assert run['head_sha']==HEAD and run['run_attempt']==1
 jobs=get('actions/runs/'+str(i)+'/attempts/1/jobs?per_page=100');assert jobs['total_count']==len(jobs['jobs'])
 arts=get('actions/runs/'+str(i)+'/artifacts?per_page=100');assert arts['total_count']==len(arts['artifacts'])
 for n,v in [('run',run),('jobs',jobs),('artifacts',arts)]:put(P/(n+'-'+str(i)+'.json'),v)
 return {'id':i,'name':run['name'],'status':run['status'],'conclusion':run['conclusion'],'attempt':run['run_attempt'],'jobs':[{'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion']} for j in jobs['jobs']],'artifacts':[{'id':a['id'],'name':a['name'],'bytes':a['size_in_bytes'],'digest':a.get('digest')} for a in arts['artifacts']]}
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as ex:summaries=list(ex.map(collect,ids))
put(P/'summary.json',summaries)
if not (R/'head-commit.json').exists():
 for n,c in [('head',HEAD),('base',BASE),('pr-merge',pr['merge_commit_sha'])]:put(R/(n+'-commit.json'),get('commits/'+c))
 files=['AGENTS.md','.github/workflows/README.md','.github/review-agent/policy.json','.github/workflows/v3-candidate-regression.yml','.github/workflows/three-node-chat-lifecycle-regression.yml','.github/workflows/native-package-preview.yml','.github/workflows/manager-browser-smoke.yml','.github/workflows/issue-agent-pr-signal.yml']
 authority={'head':HEAD,'base':BASE,'files':{},'applicable_flow':[],'workflow_invocation_mutation_rerun':False}
 for rev in [HEAD,BASE]:
  authority['files'][rev]={}
  for name in files:
   b=subprocess.check_output(['git','show',rev+':'+name]);q=R/'frozen-source'/rev/name;q.parent.mkdir(parents=True,exist_ok=True);q.write_bytes(b)
   authority['files'][rev][name]={'sha256':hashlib.sha256(b).hexdigest(),'bytes':len(b)}
 put(R/'source-authority.json',authority)
print(json.dumps({'snapshot':now,'runs':[{'id':s['id'],'status':s['status'],'conclusion':s['conclusion'],'jobs':[(j['name'],j['status'],j['conclusion']) for j in s['jobs']],'artifact_count':len(s['artifacts'])} for s in summaries]}))
