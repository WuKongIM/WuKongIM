"""Independent actual CLI checks using synthetic public API/ZIP responses only."""
import base64,copy,hashlib,io,json,os,subprocess,tempfile,zipfile
from pathlib import Path
FREEZE=Path(__file__).resolve().parent/'fixture'
OUT=Path(__file__).parent
HELPER=FREEZE/'source/scripts/chat-lifecycle/authenticate-released-preclock.py'
DOCUMENTS=json.loads((FREEZE/'source/scripts/testdata/chat-lifecycle-released-preclock/original.json').read_text())
P='/repos/WuKongIM/WuKongIM'
A='a'*40;B='b'*40

def fixture(directory, mutate=None):
 docs=copy.deepcopy(DOCUMENTS)
 if mutate:mutate(docs)
 repository={'id':638054601,'full_name':'WuKongIM/WuKongIM'}
 old={'id':100,'workflow_id':329938003,'run_number':32,'run_attempt':1,'path':'.github/workflows/chat-lifecycle-rehearsal.yml','head_branch':'main','head_sha':A,'event':'workflow_dispatch','status':'completed','conclusion':'failure','display_title':'Chat Lifecycle Rehearsal request-1','repository':repository,'head_repository':repository,'created_at':'2026-10-21T10:00:35Z','updated_at':'2026-10-21T10:19:39Z'}
 current={**old,'id':101,'run_number':33,'head_sha':B,'status':'in_progress','conclusion':None,'created_at':'2026-10-21T12:00:00Z'}
 api={P+'/actions/runs/100':old,P+'/actions/runs/101':current}
 for ident,path,title,start,end in [(102,'provision','Provision','10:10:20','10:11:59'),(103,'release','Release','10:18:12','10:19:24')]:
  api[P+f'/actions/runs/{ident}']={**old,'id':ident,'workflow_id':ident,'path':f'.github/workflows/cloud-lease-{path}.yml','display_title':f'Cloud Lease {title} request-1','conclusion':'success','created_at':f'2026-10-21T{start}Z','updated_at':f'2026-10-21T{end}Z'}
 api[P+'/actions/runs/103/jobs?per_page=100&page=1']={'total_count':1,'jobs':[{'run_id':103,'run_attempt':1,'head_sha':A,'steps':[{'name':'Release and retain residual evidence','status':'completed','conclusion':'success','started_at':'2026-10-21T10:18:30Z','completed_at':'2026-10-21T10:19:20Z'}]}]}
 api[P+'/compare/'+A+'...'+B]={'status':'ahead','base_commit':{'sha':A},'merge_base_commit':{'sha':A}}
 artifacts=[]
 for ident,run,name,contents in [(200,100,'chat-lifecycle-rehearsal-cleanup-request-1',docs),(201,102,'cloud-lease-provision-request-1',{'quote.json':docs['attempt-1/quote.json'],'receipt.json':docs['attempt-1/receipt.json']}),(202,103,'cloud-lease-release-request-1',{'release.json':docs['zero-inventory.json']})]:
  buf=io.BytesIO()
  with zipfile.ZipFile(buf,'w',zipfile.ZIP_DEFLATED) as z:
   for n,v in contents.items():z.writestr(n,json.dumps(v,separators=(',',':')))
  raw=buf.getvalue();artifact={'id':ident,'name':name,'expired':False,'size_in_bytes':len(raw),'digest':'sha256:'+hashlib.sha256(raw).hexdigest(),'workflow_run':{'id':run,'head_sha':A}}
  api[P+f'/actions/artifacts/{ident}']=artifact
  api[P+f'/actions/artifacts/{ident}/zip']={'__bytes':base64.b64encode(raw).decode()}
  api[P+f'/actions/runs/{run}/artifacts?per_page=100&page=1']={'total_count':1,'artifacts':[artifact]}
  artifacts.append(artifact)
 env=dict(os.environ,PATH=str(directory)+os.pathsep+os.environ['PATH'],GH_TOKEN='TESTONLY-not-a-credential',GITHUB_REPOSITORY='WuKongIM/WuKongIM',GITHUB_REPOSITORY_ID='638054601',GITHUB_RUN_ID='101',GITHUB_RUN_NUMBER='33',GITHUB_RUN_ATTEMPT='1',GITHUB_SHA=B,WK_CHAT_SOURCE_SHA=A,WK_CHAT_REQUEST_ID='request-1',WK_CHAT_OPERATOR='tangtaoit',WK_CHAT_RELEASED_PRECLOCK_RUN_ID='100',WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY=docs['attempt-1/run-plan.json']['bootstrap_access']['authorized_keys'][0],TEST_API=str(directory/'api.json'),TEST_CALLS=str(directory/'calls.jsonl'))
 (directory/'gh').write_text('#!/usr/bin/env python3\nimport os,sys,json,base64\na=sys.argv[1:]\nassert a[:5]==["api","--hostname","github.com","--method","GET"] and len(a)==6\nwith open(os.environ["TEST_CALLS"],"a") as f:f.write(json.dumps(a)+"\\n")\nv=json.load(open(os.environ["TEST_API"])).get(a[5])\nif v is None:sys.exit(93)\nif isinstance(v,dict) and "__bytes" in v:sys.stdout.buffer.write(base64.b64decode(v["__bytes"]))\nelse:print(json.dumps(v))\n')
 (directory/'gh').chmod(0o700)
 return docs,api,artifacts,env

def run(directory,api,artifacts,env,mode='prepare'):
 (directory/'api.json').write_text(json.dumps(api));(directory/'inventory.json').write_text(json.dumps({'complete':True,'artifacts':artifacts}))
 args=['python3',str(HELPER),mode]
 if mode=='prepare':args+=['--inventory',str(directory/'inventory.json'),'--output',str(directory/'continuation')]
 else:args+=['--directory',str(directory),'--run-plan',str(directory/'plan.json')]
 r=subprocess.run(args,env=env,capture_output=True,text=True,timeout=10)
 result={'exit':r.returncode,'receipt':json.loads(r.stdout),'stderr':r.stderr,'sidecar_exists':(directory/'continuation').exists()}
 calls=(directory/'calls.jsonl').read_text().splitlines() if (directory/'calls.jsonl').exists() else []
 result['api_calls']=len(calls);result['only_exact_gets']=all(json.loads(x)[:5]==['api','--hostname','github.com','--method','GET'] for x in calls)
 return result

results={}
for case in ['baseline','typed_runtime_terminal','typed_grant_terminal','duplicate_zero_scope','later_consumer_completed','default_spent_completed','default_spent_in_progress','default_no_history']:
 with tempfile.TemporaryDirectory(prefix='authority-review-') as d:
  d=Path(d)
  def mutate(docs):
   if case.startswith('typed_'):
    docs['pre-clock-terminal.json']={'schema':'wukongim.chat_lifecycle.pre_clock_terminal/v1','request_id':'request-1','stage':'rehearsal','coordinator_code':'runtime' if 'runtime' in case else 'grant'}
   if case=='duplicate_zero_scope':docs['zero-inventory.json']['result']['zero_inventory']['scopes'].append('instances')
  docs,api,artifacts,env=fixture(d,mutate)
  if case.startswith('default_'):
   env['WK_CHAT_RELEASED_PRECLOCK_RUN_ID']=''
   if case=='default_no_history':artifacts=[]
   else:
    # Exact protected finalizer identity; same cleanup name. Model handoff
    # deletion after zero upload but before the scheduled producer completes.
    api[P+'/actions/runs/100']['path']='.github/workflows/chat-lifecycle-rehearsal-finalize.yml'
    api[P+'/actions/runs/100']['event']='schedule'
    api[P+'/actions/runs/100']['status']='in_progress' if case.endswith('in_progress') else 'completed'
  results[case]=run(d,api,artifacts,env)
  if case.startswith('typed_') and (d/'continuation/evidence/original').exists():
   results[case]['typed_terminal_preserved']=(d/'continuation/evidence/original/pre-clock-terminal.json').exists()
  if case=='later_consumer_completed':
   assert results[case]['receipt']['status']=='ready'
   plan=copy.deepcopy(docs['attempt-1/run-plan.json']);plan['attempt']=2;plan['lease_plan']['lease_id']='request-1-rehearsal-2';plan['lease_plan']['budget']['committed_micros']=74477600
   (d/'plan.json').write_text(json.dumps(plan))
   api[P+'/actions/runs/101'].update(status='completed',conclusion='success',updated_at='2026-10-21T12:10:00Z')
   env.update(GITHUB_RUN_ID='999',GITHUB_RUN_NUMBER='7',GITHUB_SHA='e'*40)
   results[case]['validate']=run(d,api,artifacts,env,'validate')
record={'helper_sha256':hashlib.sha256(HELPER.read_bytes()).hexdigest(),'real_cli':True,'synthetic_api_zip_only':True,'cloud_calls':False,'results':results}
(OUT/'replay-results.json').write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps(record,indent=2))
