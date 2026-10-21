#!/usr/bin/env python3
"""Exact-source GET-only admission snapshot. No provider/workflow mutation."""
import concurrent.futures,datetime,hashlib,json,os,secrets,subprocess
from pathlib import Path
D=Path(__file__).parent; B=D.parent; R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
C='cee5d51f325fb84033060f546337d326110adae5'; P=B/'next-request-after-pr1022-preparation/generation-3'
PRIVATE=B/'rc-new-cee5-private'; PRIVATE.mkdir(mode=0o700,exist_ok=False)
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
request='chat-'+datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')+'-'+secrets.token_hex(4)
save('authorization.json',{'schema':'wukongim.cloud_rc.paid_authorization/v1','recorded_at':now(),'human_answer_verbatim':'授权按此方案启动新的云 RC','human_answer_source':'explicit human reply relayed by parent /root in the current conversation','source_sha':C,'control_sha':C,'request_id':request,'scope':'New first-attempt request. Hangzhou four 4-vCPU/8GiB hosts (three server nodes plus one load host), 256 slots; 2h rehearsal and release, then fresh Lease for continuous72h soak, capacity<=8h and30min recovery; temporary public SSH/HTTP, unique tracking Issue, safety schedules and final exact zero cleanup. Admission failure stops.','prior_quote_reserve_micros':148955200,'request_budget_limit_micros':'1351044800','operational_stop_micros':'1201044800','aggregate_hard_limit_micros':1500000000,'aggregate_operational_stop_micros':1350000000,'reserve_is_not_provider_invoice':True,'new_budget_or_iam_changes_authorized':False,'dispatch_owner':'/root','subtask_scope':'GET-only checks, new local request-scoped diagnostic SSH identity and one exact tracking Issue; no dispatch, provider calls or workflow/configuration changes','workflow_route':'Exact formal Actions authorization; Direct Lab is a different diagnostic contract and is not selected','old_request_reuse':False,'released_preclock_run_id':''})
plan=json.loads((P/'source-and-budget.json').read_text()); require(plan['source_sha']==plan['control_sha']==C,'frozen plan source mismatch')
require(len(plan['rule_and_source_files'])==31,'frozen plan file count mismatch')
require(plan['request_budget_limit_micros']=='1351044800' and plan['operational_stop_micros']==1201044800 and plan['prior_quote_reserve_micros']==148955200,'frozen budgets mismatch')
for e in json.loads((P/'manifest.json').read_text())['files']:
 b=(P/e['path']).read_bytes();require(h(b)==e['sha256'] and len(b)==e['bytes'],'frozen plan file mismatch')
rows=[]
for e in plan['rule_and_source_files']:
 b=blob(e['path']);require(h(b)==e['sha256'] and len(b)==e['bytes'],'frozen source file mismatch: '+e['path']);rows.append(e)
extra=[]
for p in ['docs/agents/issue-tracker.md','scripts/chat-lifecycle/comment-request-issue.sh']:
 b=blob(p);extra.append({'path':p,'sha256':h(b),'bytes':len(b)})
 (D/p.replace('/','__')).write_bytes(b)
tree=subprocess.check_output(['git','rev-parse',C+'^{tree}'],cwd=R,text=True).strip();require(tree==plan['tree'],'exact source tree mismatch')
save('rules-and-source-freeze.json',{'source_sha':C,'control_sha':C,'tree':tree,'rule_and_source_files':rows,'additional_read_contracts':extra,'generation_3_manifest_sha256':h((P/'manifest.json').read_bytes()),'generation_3_source_and_budget_sha256':h((P/'source-and-budget.json').read_bytes()),'all_31_match':True})
main=get('main-before','repos/WuKongIM/WuKongIM/git/ref/heads/main');require(main['object']['sha']==C,'remote main changed')
old=json.loads((B/'rc-readiness-fc182-current/summary.json').read_text());expected={x['name']:x['value_sha256'] for x in old['identity_variables']};require(len(expected)==8,'expected identity inventory mismatch')
def variable(name):
 v=get(name,'repos/WuKongIM/WuKongIM/actions/variables/'+name,hash_variable=True)
 return v|{'matches_frozen_fc182':v['value_sha256']==expected[name]}
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as ex:vars=list(ex.map(variable,expected))
require(all(v['present'] and v['matches_frozen_fc182'] for v in vars),'identity/wrapping public variable mismatch')
subject=get('oidc-subject','repos/WuKongIM/WuKongIM/actions/oidc/customization/sub');require(subject==old['oidc_subject'],'OIDC subject changed')
workflow_names=['chat-lifecycle-rehearsal.yml','chat-lifecycle-rehearsal-finalize.yml','chat-lifecycle-formal.yml','chat-lifecycle-formal-finalize.yml','chat-lifecycle-stop.yml','cloud-deployment-bundle.yml','cloud-deployment-activate.yml','cloud-lease-provision.yml','cloud-lease-release.yml','cloud-lease-observe.yml']
def wf(name):return name,get('workflow-'+name,'repos/WuKongIM/WuKongIM/actions/workflows/'+name)
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as ex:workflows=dict(ex.map(wf,workflow_names))
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
