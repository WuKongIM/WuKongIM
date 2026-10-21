from pathlib import Path
import copy,hashlib,json,os,subprocess,tempfile
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-budget-downward')
OUT=Path(__file__).parent
helper=ROOT/'scripts/chat-lifecycle/authenticate-released-preclock.py'
rows=[]
def invoke(args):
 p=subprocess.run(['python3','-B',str(helper),*args],capture_output=True,text=True,timeout=5)
 return {'exit':p.returncode,'stdout':p.stdout,'stderr':p.stderr}
for value in ['1500000000','1351044800','150000001','150000000','0','-1','1500000001','9223372036854775808','1351044800.0','1e9','+1351044800','01351044800',' 1351044800','1351044800 ','']:
 r=invoke(['budget-input','--limit='+value]);want=value in ['1500000000','1351044800','150000001'];assert (r['exit']==0)==want;rows.append({'kind':'input','value':value,'expected_accept':want,**r})
for value in ['1500000000','1351044800']:
 r=invoke(['budget-input','--limit='+value,'--released-preclock-run-id=100']);assert (r['exit']==0)==(value=='1500000000');rows.append({'kind':'continuation_input','value':value,**r})
with tempfile.TemporaryDirectory(prefix='probe-',dir=OUT) as tmp:
 d=Path(tmp);H=1351044800;S=H-150000000
 budget={'currency':'CNY','limit_micros':H,'operational_stop_micros':S,'committed_micros':0}
 base={'plan':{'schema':'wukongim.chat_lifecycle.run_plan/v1','stage':'rehearsal','attempt':1,'operational_stop_micros':S,'lease_plan':{'request_id':'probe','lease_id':'probe-rehearsal-1','budget':budget}},'receipt':{'schema':'wukongim.cloud_lease.receipt/v1','receipt':{'request_id':'probe','lease_id':'probe-rehearsal-1','budget':copy.deepcopy(budget)}},'deployment':{'schema':'wukongim.cloud_deployment.plan/v2','request_id':'probe','lease_id':'probe-rehearsal-1','budget':copy.deepcopy(budget)}}
 mutations={'valid':None,'root-stop':('plan',['operational_stop_micros'],1350000000),'receipt-cap':('receipt',['receipt','budget','limit_micros'],1500000000),'receipt-bool':('receipt',['receipt','budget','committed_micros'],False),'deployment-stop':('deployment',['budget','operational_stop_micros'],1350000000),'lease-bool':('plan',['lease_plan','budget','committed_micros'],False),'currency':('plan',['lease_plan','budget','currency'],'USD'),'float-cap':('plan',['lease_plan','budget','limit_micros'],float(H))}
 for name,mutation in mutations.items():
  docs=copy.deepcopy(base)
  if mutation:
   doc,keys,v=mutation;current=docs[doc]
   for key in keys[:-1]:current=current[key]
   current[keys[-1]]=v
  for key,doc in docs.items():(d/(key+'.json')).write_text(json.dumps(doc))
  r=invoke(['budget','--run-plan',str(d/'plan.json'),'--receipt',str(d/'receipt.json'),'--deployment-plan',str(d/'deployment.json')]);assert (r['exit']==0)==(name=='valid');rows.append({'kind':'reader','mutation':name,**r})
 for key,doc in base.items():(d/(key+'.json')).write_text(json.dumps(doc))
 r=invoke(['validate','--directory',str(d),'--run-plan',str(d/'plan.json')]);assert r['exit']==0;rows.append({'kind':'first_start_no_sidecar',**r})
 (d/'continuation').mkdir();r=invoke(['validate','--directory',str(d),'--run-plan',str(d/'plan.json')]);assert r['exit']==1;rows.append({'kind':'lower_with_sidecar',**r})
findings=[]
for stage,step,variable,cap,amounts in [('rehearsal','transition','committed_micros',1201044800,[1201044799,1201044800]),('formal','complete','aggregate_conservative_micros',1351044800,[1351044800,1351044801])]:
 path=ROOT/'.github/workflows'/('chat-lifecycle-'+stage+'-finalize.yml');source=path.read_text();line=next(x.strip() for x in source.splitlines() if '((' in x and variable in x and '|| exit 1' in x)
 capvar='operational_stop_micros' if stage=='rehearsal' else 'request_budget_limit_micros'
 for value in amounts:
  p=subprocess.run(['/bin/bash','-c','set -euo pipefail\n'+line],env={**os.environ,variable:str(value),capvar:str(cap)},capture_output=True,text=True,timeout=3)
  rows.append({'kind':'exact_source_budget_expression','stage':stage,'line':line,'amount':value,'cap':cap,'exit':p.returncode})
 pubname='Upload zero-inventory proof' if stage=='rehearsal' else 'Upload formal zero-inventory proof'
 publines=source[source.index('      - name: '+pubname):].splitlines()
 condition=publines[1].strip().removeprefix('if: ')
 assert condition=="steps.cleanup.outputs.zero == 'true'"
 findings.append({'stage':stage,'failing_step':step,'source_condition':condition,'explicit_status_function':False,'after_budget_exit1':{'cleanup_zero':True,'prior_step_success':False,'effective_upload_condition':"success() && steps.cleanup.outputs.zero == 'true'",'upload_runs':False},'scope':'Boolean consequence of documented Actions status semantics, not a remote Actions run'})
result={'checks':rows,'findings':findings,'external_actions_executed':False,'real_cloud_qualification':False,'historical_reserve_arithmetic':{'reserve':148955200,'newH':1351044800,'newS':1201044800,'reserve_plus_H':148955200+1351044800,'reserve_plus_S':148955200+1201044800}}
(OUT/'probe-result.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({'checks':len(rows),'expected_failures_observed':sum(x.get('exit',0)!=0 for x in rows),'finding_scenarios':findings},indent=2))
