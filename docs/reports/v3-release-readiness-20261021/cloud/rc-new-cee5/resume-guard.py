#!/usr/bin/env python3
"""Exact-source GET-only admission snapshot. No provider/workflow mutation."""
import concurrent.futures,datetime,hashlib,json,os,secrets,subprocess
from pathlib import Path
D=Path(__file__).parent; B=D.parent; R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
C='cee5d51f325fb84033060f546337d326110adae5'; P=B/'next-request-after-pr1022-preparation/generation-3'
PRIVATE=B/'rc-new-cee5-private'; assert PRIVATE.is_dir()
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def h(v):return hashlib.sha256(v).hexdigest()
def save(n,v):
 p=D/n;p.parent.mkdir(parents=True,exist_ok=True)
 with p.open('x') as f:json.dump(v,f,ensure_ascii=False,indent=2);f.write('\n')
def require(v,m):
 if not v:raise RuntimeError(m)
def blob(p):return subprocess.check_output(['git','show',C+':'+p],cwd=R)
def get(name,endpoint,fields=(),hash_variable=False):
 cmd=['gh','api','--hostname','github.com','--method','GET',endpoint]
 for k,v in fields:cmd+=['-f',k+'='+v]
 t=now()
 try:r=subprocess.run(cmd,cwd=R,capture_output=True,timeout=60)
 except subprocess.TimeoutExpired as e:
  raw=e.stderr or b''; receipt={'started_at':t,'observed_at':now(),'endpoint':endpoint,'status':'timeout','stderr_sha256':h(raw)}
  save('reads/'+name+'-failure.json',receipt);raise RuntimeError('GET timed out: '+name)
 if r.returncode:
  p=PRIVATE/(name+'.stderr');p.write_bytes(r.stderr);p.chmod(0o600)
  save('reads/'+name+'-failure.json',{'started_at':t,'observed_at':now(),'endpoint':endpoint,'exit_code':r.returncode,'stdout_sha256':h(r.stdout),'stderr_sha256':h(r.stderr),'raw_stderr_public':False})
  raise RuntimeError('GET failed: '+name)
 value=json.loads(r.stdout)
 rec={'started_at':t,'observed_at':now(),'endpoint':endpoint,'exit_code':0,'response_bytes':len(r.stdout),'response_sha256':h(r.stdout)}
 if hash_variable:
  require(value.get('name')==name and isinstance(value.get('value'),str),'variable response invalid')
  data={'name':name,'present':bool(value['value']),'value_sha256':h(value['value'].encode())}
  rec['data']=data
 else:rec['data']=value
 save('reads/'+name+'.json',rec)
 return rec['data']

authorization=json.loads((D/'authorization.json').read_text());request=authorization['request_id']
freeze=json.loads((D/'rules-and-source-freeze.json').read_text());tree=freeze['tree']
old=json.loads((B/'rc-readiness-fc182-current/summary.json').read_text());expected={x['name']:x['value_sha256'] for x in old['identity_variables']}
vars=[]
for name,digest in expected.items():
 v=json.loads((D/'reads'/f'{name}.json').read_text())['data'];require(v['present'] and v['value_sha256']==digest,'identity mismatch');vars.append(v|{'matches_frozen_fc182':True})
require(json.loads((D/'reads/oidc-subject.json').read_text())['data']==old['oidc_subject'],'OIDC subject mismatch')
workflow_names=['chat-lifecycle-rehearsal.yml','chat-lifecycle-rehearsal-finalize.yml','chat-lifecycle-formal.yml','chat-lifecycle-formal-finalize.yml','chat-lifecycle-stop.yml','cloud-deployment-bundle.yml','cloud-deployment-activate.yml','cloud-lease-provision.yml','cloud-lease-release.yml','cloud-lease-observe.yml']
workflows={}
for name in workflow_names:
 p=D/'reads'/('workflow-'+name+'.json')
 if p.exists():workflows[name]=json.loads(p.read_text())['data']
 else:
  raw=(PRIVATE/('workflow-'+name+'.stderr')).read_bytes()
  save('reads/'+name+'-retry-reason.json',{'original_stderr_sha256':h(raw),'transport_EOF':b'EOF' in raw,'retry':'one read-only GET; no failed CI or mutation is retried'})
  workflows[name]=get('workflow-'+name+'-read-2','repos/WuKongIM/WuKongIM/actions/workflows/'+name)
for n in ['chat-lifecycle-rehearsal.yml','chat-lifecycle-stop.yml','cloud-deployment-bundle.yml','cloud-deployment-activate.yml','cloud-lease-provision.yml','cloud-lease-release.yml','cloud-lease-observe.yml']:
 require(workflows[n]['state']=='active','required workflow is not active: '+n)
for n in ['chat-lifecycle-rehearsal-finalize.yml','chat-lifecycle-formal.yml','chat-lifecycle-formal-finalize.yml']:
 require(workflows[n]['state'] in ('active','disabled_manually'),'unexpected safety workflow state: '+n)
latest=get('latest-top','repos/WuKongIM/WuKongIM/actions/workflows/chat-lifecycle-rehearsal.yml/runs',[('per_page','1')])['workflow_runs'][0]
require(latest['workflow_id']==workflows['chat-lifecycle-rehearsal.yml']['id'] and latest['run_number']==33 and latest['run_attempt']==1 and latest['status']=='completed','latest paid top workflow is not terminal original #33')
prior=get('top-33','repos/WuKongIM/WuKongIM/actions/runs/'+str(latest['id']));require(prior['id']==latest['id'] and prior['status']=='completed' and prior['run_number']==33 and prior['run_attempt']==1,'top33 changed')
query='repo:WuKongIM/WuKongIM is:issue in:title "[chat-lifecycle] '+request+'"'
search=get('issue-before','search/issues',[('q',query),('per_page','100')]);require(search['incomplete_results'] is False and search['total_count']==len(search['items'])==0,'request already has issue or search incomplete')
main2=get('main-after','repos/WuKongIM/WuKongIM/git/ref/heads/main');require(main2['object']['sha']==C,'remote main changed during checks')
summary={'observed_at':now(),'guard_pass':True,'request_id':request,'source_sha':C,'control_sha':C,'tree':tree,'all_31_frozen_files_match':True,'main_before_and_after_exact':True,'identity_variables':vars,'seven_identity_plus_wrapping_unchanged':True,'oidc_subject_unchanged':True,'latest_top':{k:prior[k] for k in ['id','run_number','run_attempt','status','conclusion','head_sha','html_url']},'workflow_states':{n:{k:w[k] for k in ['id','state','path']} for n,w in workflows.items()},'safety_schedule_note':'Idle finalizers/starter may be manually disabled by their owner. Exact rehearsal source enables rehearsal finalizer before paid orchestration; formal and finalizer owners arm their next stage. Generic release sweep is active. This preparation changes none.','issue_before_count':0,'provider_calls':False,'workflow_state_changes':False,'dispatched':False,'limits':['Metadata hashes do not prove live IAM or wrapping-secret usability','Current provider zero is not independently re-proved here; old two leases zero relayed by parent, runtime guard remains authoritative','main/ref checks are point-in-time, not atomic dispatch SHA CAS','No paid attempt, OIDC apply, Quote or cloud inventory performed']}
save('fresh-public-guard.json',summary)
print(json.dumps({k:summary[k] for k in ['guard_pass','request_id','source_sha','latest_top','workflow_states']},indent=2))
