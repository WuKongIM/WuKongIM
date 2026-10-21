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

A=json.loads((D/'authorization.json').read_text());request=A['request_id'];title='[chat-lifecycle] '+request
create=json.loads((D/'issue-create-result.json').read_text());require(create['exit_code']==0 and create['number']==1023,'original create not proven')
issue=get('issue-exact-read-2','repos/WuKongIM/WuKongIM/issues/'+str(create['number']))
require(issue['title']==title and issue['state']=='open' and issue['locked'] is False and 'pull_request' not in issue and issue['repository_url']=='https://api.github.com/repos/WuKongIM/WuKongIM' and isinstance(issue['comments'],int),'created Issue identity incomplete')
require(issue['body'].rstrip()==(D/'issue-body.md').read_text().rstrip(),'created Issue body mismatch')
query='repo:WuKongIM/WuKongIM is:issue in:title "'+title+'"'
search=get('issue-search-read-2','search/issues',[('q',query),('per_page','100')])
require(search['incomplete_results'] is False and search['total_count']==len(search['items'])==1 and search['items'][0]['id']==issue['id'] and search['items'][0]['title']==title,'exact Issue search not yet unique; read reconcile required')
main2=get('main-post-issue-create','repos/WuKongIM/WuKongIM/git/ref/heads/main');require(main2['object']['sha']==C,'remote main changed after Issue creation')
save('issue-verification.json',{'observed_at':now(),'request_id':request,'title':title,'id':issue['id'],'number':issue['number'],'url':issue['html_url'],'state':issue['state'],'locked':issue['locked'],'comments':issue['comments'],'repository_url':issue['repository_url'],'exact_search_count':1,'search_complete':True,'body_matches':True,'main_exact':True,'verification':'official gh GET and exact-title complete Search; not execution of the environment-token preflight shell','issue_create_attempts':1,'dispatched':False})
save('planned-dispatch.json',{'schema':'wukongim.cloud_rc.planned_dispatch/v1','prepared_at':now(),'executed':False,'dispatch_owner':'/root','repository':'WuKongIM/WuKongIM','workflow':'.github/workflows/chat-lifecycle-rehearsal.yml','ref':'main','source_sha':C,'control_sha':C,'inputs_file':'planned-workflow-inputs.json','inputs_sha256':h((D/'planned-workflow-inputs.json').read_bytes()),'input_count':6,'request_id':request,'tracking_issue':issue['html_url'],'prior_quote_reserve_micros':148955200,'request_budget_limit_micros':'1351044800','operational_stop_micros':1201044800,'provider_calls':False,'workflow_or_permission_changes':False,'next_action':'root fresh exact main guard and single authorized dispatch; no subagent dispatch'})
print(json.dumps({'request_id':request,'issue':issue['html_url'],'main':C,'inputs':str(D/'planned-workflow-inputs.json'),'dispatched':False}))
