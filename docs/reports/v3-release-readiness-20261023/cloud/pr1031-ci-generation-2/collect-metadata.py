from pathlib import Path
import concurrent.futures,datetime,hashlib,json,os,subprocess,sys
B=Path(__file__).parent;P=B.with_name(B.name+'-private');HEAD='2662fe93b754d614422f2936185cf781a33b831a';BASE='8dfcd35b7d604fb5d3da3d75e36ab267e2f4b1bf';REPO='WuKongIM/WuKongIM'
now=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat()
sha=lambda b:hashlib.sha256(b).hexdigest()
O=B/'polls'/datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');O.mkdir(parents=True);Q=P/'polls'/O.name;Q.mkdir(parents=True,mode=0o700)
def write(p,v):
 with p.open('x') as f:json.dump(v,f,indent=2);f.write('\n')
def keep(x,keys):return {k:x.get(k) for k in keys}
RK=['id','name','path','head_sha','head_branch','event','run_number','run_attempt','status','conclusion','created_at','updated_at','run_started_at','html_url','workflow_id','check_suite_id']
def project(k,v):
 if k=='run':return keep(v,RK)
 if k=='runs':return {'total_count':v['total_count'],'workflow_runs':[keep(x,RK) for x in v['workflow_runs']]}
 if k=='jobs':return {'total_count':v['total_count'],'jobs':[dict(keep(j,['id','run_id','run_attempt','head_sha','name','status','conclusion','started_at','completed_at','html_url','runner_id','runner_name','runner_group_name']),steps=[keep(s,['name','number','status','conclusion','started_at','completed_at']) for s in j.get('steps',[])]) for j in v['jobs']]}
 if k=='artifacts':return {'total_count':v['total_count'],'artifacts':[keep(a,['id','name','size_in_bytes','digest','expired','created_at','updated_at','expires_at','workflow_run']) for a in v['artifacts']]}
 if k=='checks':return {'total_count':v['total_count'],'check_runs':[keep(x,['id','name','head_sha','status','conclusion','started_at','completed_at','html_url','details_url']) for x in v['check_runs']]}
 if k=='pr':return dict(keep(v,['number','state','draft','mergeable','mergeable_state','merged','merged_at','merge_commit_sha','updated_at','html_url']),head=keep(v['head'],['sha','ref']),base=keep(v['base'],['sha','ref']))
 if k=='workflow':return keep(v,['id','name','path','state','created_at','updated_at','html_url'])
 if k=='commit':return {'sha':v['sha'],'tree':v['commit']['tree']['sha'],'parents':[x['sha'] for x in v['parents']]}
 raise ValueError(k)
def get(item):
 name,endpoint,kind=item;started=now()
 try:r=subprocess.run(['gh','api','--method','GET','repos/'+REPO+'/'+endpoint],capture_output=True,timeout=55);code=r.returncode;out=r.stdout;err=r.stderr;timeout=False
 except subprocess.TimeoutExpired as e:code=None;out=e.stdout or b'';err=e.stderr or b'';timeout=True
 for suffix,data in [('stdout',out),('stderr',err)]:
  p=Q/(name+'.'+suffix)
  with p.open('xb') as f:os.chmod(p,0o600);f.write(data)
 rec={'started_at':started,'finished_at':now(),'method':'GET','endpoint':endpoint,'exit_code':code,'timeout':timeout,'stdout_bytes':len(out),'stdout_sha256':sha(out),'stderr_bytes':len(err),'stderr_sha256':sha(err),'raw_response':'private sibling excluded','retry_count':0}
 v=None
 if code==0 and len(out)<=4<<20:
  v=project(kind,json.loads(out));write(O/(name+'.json'),v);rec['public_json_sha256']=sha((O/(name+'.json')).read_bytes())
 write(O/(name+'.read.json'),rec);return name,v,rec
_,discovery,_=get(('discovery','actions/runs?head_sha='+HEAD+'&per_page=100','runs'));assert discovery and discovery['total_count']<=100
runs=discovery['workflow_runs'];assert runs and all(r['head_sha']==HEAD and r['run_attempt']==1 for r in runs)
items=[]
for r in runs:
 rid=r['id'];items += [(f'run-{rid}',f'actions/runs/{rid}','run'),(f'jobs-{rid}',f'actions/runs/{rid}/attempts/1/jobs?per_page=100','jobs'),(f'artifacts-{rid}',f'actions/runs/{rid}/artifacts?per_page=100','artifacts')]
items += [('pr','pulls/1031','pr'),('checks','commits/'+HEAD+'/check-runs?per_page=100','checks')]
if len(list((B/'polls').iterdir()))==1:
 items += [('workflow-'+f,'actions/workflows/'+f,'workflow') for f in ['review-agent-pr-signal.yml','review-agent.yml','review-agent-run.yml']]
 items += [('source-head','commits/'+HEAD,'commit'),('source-base','commits/'+BASE,'commit')]
 # Old generation is metadata only, never evidence for new head.
 for rid in [39877882441,39877882453,39877882456,39877882484]:items += [(f'old-run-{rid}',f'actions/runs/{rid}','run'),(f'old-jobs-{rid}',f'actions/runs/{rid}/attempts/1/jobs?per_page=100','jobs')]
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:results=list(pool.map(get,items))
lookup={n:v for n,v,r in results}
for n,v in lookup.items():
 if v and 'total_count' in v:assert v['total_count']<=100
pr=lookup['pr'];assert pr is None or (pr['head']['sha']==HEAD and pr['base']['sha']==BASE)
if pr and pr['merge_commit_sha']:
 n,v,r=get(('source-merge','commits/'+pr['merge_commit_sha'],'commit'));results.append((n,v,r))
summary={'observed_at':now(),'head':HEAD,'base':BASE,'runs':[{**lookup['run-'+str(r['id'])],'jobs':lookup['jobs-'+str(r['id'])]['jobs']} for r in runs if lookup.get('run-'+str(r['id'])) and lookup.get('jobs-'+str(r['id']))], 'GET_failures':[{'name':n,**r} for n,v,r in results if v is None],'all_terminal':all(lookup.get('run-'+str(r['id']),{}).get('status')=='completed' for r in runs),'all_owned_processes_terminal':True}
write(O/'summary.json',summary)
print(json.dumps({'snapshot':str(O),'all_terminal':summary['all_terminal'],'GET_failures':[x['name'] for x in summary['GET_failures']],'runs':[{'id':r['id'],'name':r['name'],'status':r['status'],'conclusion':r['conclusion'],'jobs':[keep(j,['id','name','status','conclusion']) for j in r['jobs']]} for r in summary['runs']]}))
