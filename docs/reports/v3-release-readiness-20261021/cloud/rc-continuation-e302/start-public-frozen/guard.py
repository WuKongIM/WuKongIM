import concurrent.futures,datetime,hashlib,json,subprocess
from pathlib import Path
D=Path(__file__).parent; P=D.parent/'rc-next-execution-plan-post1020'; C='e30299fa59ea6669ea71024eabe209840f241f19'; R='WuKongIM/WuKongIM'
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def save(n,v):(D/n).write_text(json.dumps(v,indent=2)+'\n')
authorization={'schema':'wukongim.rc_continuation_human_authorization/v1','recorded_at':now(),'human_answer':'授权按此方案续跑','evidence_source':'Explicit human reply reported by parent /root in current conversation; no new authority inferred from tool output','control_sha':C,'source_sha':'fc18221282f289088ec9b4282fda8e0bfc67a3b8','request_id':'chat-20261021T095421Z-cf11d65c','scope':'One exact formal workflow continuation, Hangzhou 4 hosts each 4 vCPU/8GiB; 2h rehearsal then release and fresh 72h formal + capacity <=8h + 30min recovery; original request tracking and exact cleanup','budget_cny':1500,'operational_stop_cny':1350,'inherited_quote_reserve_cny':'74.4776','actual_old_bill':None,'no_extra_permission_or_budget':True,'no_product_source_change':True,'no_manual_retries':True,'skill_resolution':'Exact user authorization selects formal workflow contract; Direct Lab skill describes a different diagnostic flow and does not replace this authorization.'}
save('authorization.json',authorization)
for n in ['planned-workflow-inputs.json','planned-dispatch.json']:(D/n).write_bytes((P/n).read_bytes())
freeze=json.loads((P/'source-and-rule-freeze.json').read_text()); rows=[]
for e in freeze['files']:
 b=subprocess.check_output(['git','show',C+':'+e['path']]);assert hashlib.sha256(b).hexdigest()==e['sha256'];rows.append(e)
for p in ['.agents/skills/wukongim-chat-lifecycle/SKILL.md','.agents/skills/wukongim-chat-lifecycle/references/operator-workflow.md']:
 b=subprocess.check_output(['git','show',C+':'+p]);rows.append({'path':p,'source_sha':C,'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest()})
save('rules-and-source-freeze.json',{'observed_at':now(),'files':rows,'plan_manifest_sha256':hashlib.sha256((P/'SHA256SUMS').read_bytes()).hexdigest()})
endpoints={'main':'commits/main','top-workflow':'actions/workflows/329938003','latest-top':'actions/workflows/329938003/runs?per_page=1','prior-run':'actions/runs/39581355444','issue':'issues/1018','rehearsal-finalizer':'actions/workflows/chat-lifecycle-rehearsal-finalize.yml','formal-starter':'actions/workflows/chat-lifecycle-formal.yml','formal-finalizer':'actions/workflows/chat-lifecycle-formal-finalize.yml','lease-sweep':'actions/workflows/cloud-lease-release.yml'}
def get(k,path):
 t=now();p=subprocess.run(['gh','api','repos/'+R+'/'+path],capture_output=True,text=True,timeout=60);receipt={'observed_at':t,'endpoint':path,'exit_code':p.returncode};
 if p.returncode: receipt['stderr']=p.stderr;save('guard-'+k+'-error.json',receipt);return k,None
 v=json.loads(p.stdout);save('guard-'+k+'.json',v);receipt['sha256']=hashlib.sha256(p.stdout.encode()).hexdigest();return k,(v,receipt)
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as e:result=dict(e.map(lambda kv:get(*kv),endpoints.items()))
assert all(v is not None for v in result.values()),'One or more GETs failed; no dispatch'
v={k:t[0] for k,t in result.items()}; latest=v['latest-top']['workflow_runs'][0];prior=v['prior-run']; issue=v['issue']
assert v['main']['sha']==C
assert latest['id']==39581355444 and latest['run_number']==32 and latest['run_attempt']==1
assert prior['id']==39581355444 and prior['run_attempt']==1 and prior['run_number']==32 and prior['status']=='completed'
assert v['top-workflow']['state']=='active' and v['top-workflow']['id']==329938003
assert issue['title']=='[chat-lifecycle] chat-20261021T095421Z-cf11d65c' and issue['state']=='open' and not issue['locked']
assert subprocess.run(['git','merge-base','--is-ancestor','fc18221282f289088ec9b4282fda8e0bfc67a3b8',C]).returncode==0
summary={'observed_at':now(),'ready_for_single_dispatch':True,'control_sha':C,'latest_run':{k:latest[k] for k in ['id','run_number','run_attempt','status','conclusion','head_sha']},'issue':{k:issue[k] for k in ['number','title','state','locked']},'scheduler_states':{k:{'id':v[k]['id'],'state':v[k]['state'],'path':v[k]['path']} for k in ['rehearsal-finalizer','formal-starter','formal-finalizer','lease-sweep']},'receipts':{k:t[1] for k,t in result.items()},'limitations':['Direct issue GET is not unique-issue preflight; workflow rechecks','No live cloud inventory or OIDC call by this local guard','ref main API is not an atomic SHA compare-and-swap']}
save('fresh-guard-summary.json',summary);print(json.dumps({k:summary[k] for k in ['observed_at','ready_for_single_dispatch','control_sha','latest_run','issue','scheduler_states']},indent=2))
