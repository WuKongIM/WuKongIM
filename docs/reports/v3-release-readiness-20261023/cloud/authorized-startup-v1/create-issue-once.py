#!/usr/bin/env python3
"""Exact-source GET-only admission snapshot. No provider/workflow mutation."""
import concurrent.futures,datetime,hashlib,json,os,secrets,subprocess
from pathlib import Path
D=Path(__file__).parent; B=D.parent; R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
C='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06'; P=B/'next-request-after-pr1024-merged-preparation'
PRIVATE=B/'pr1024-authorized-run-resume-20261023-private'; assert PRIVATE.is_dir()
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

import re
A=json.loads((B/'pr1024-authorized-run-v1/authorization.json').read_text());request=A['request_id'];title='[chat-lifecycle] '+request
assert json.loads((D/'guard/fresh-public-guard.json').read_text())['guard_pass']
assert json.loads((D/'local-diagnostic-identity.json').read_text())['public_private_match']
remote=subprocess.check_output(['git','config','--get','remote.origin.url'],cwd=R,text=True).strip()
require(remote in ('git@github.com:WuKongIM/WuKongIM.git','https://github.com/WuKongIM/WuKongIM.git','https://github.com/WuKongIM/WuKongIM'),'unexpected issue repository remote')
query='repo:WuKongIM/WuKongIM is:issue in:title "'+title+'"'
main=get('main-pre-issue-create','repos/WuKongIM/WuKongIM/git/ref/heads/main');require(main['object']['sha']==C,'remote main changed')
search=get('issue-pre-create','search/issues',[('q',query),('per_page','100')]);require(search['incomplete_results'] is False and search['total_count']==len(search['items'])==0,'request issue not unique/absent')
cmd=['gh','issue','create','--title',title,'--body-file',str(D/'issue-body.md')]
save('issue-create-attempt.json',{'started_at':now(),'repository':'WuKongIM/WuKongIM','request_id':request,'title':title,'body_sha256':h((D/'issue-body.md').read_bytes()),'command':cmd,'attempt':1,'authorization_file':'authorization.json','unknown_outcome_policy':'read reconciliation only; never automatically resend'})
try:
 r=subprocess.run(cmd,cwd=R,capture_output=True,timeout=60)
except subprocess.TimeoutExpired as e:
 raw=e.stderr or b'';p=PRIVATE/'issue-create.stderr';p.write_bytes(raw);p.chmod(0o600)
 save('issue-create-result.json',{'observed_at':now(),'status':'unknown-timeout','stderr_sha256':h(raw),'raw_stderr_public':False})
 raise RuntimeError('Issue create outcome unknown; reconcile reads only, do not resend')
if r.stderr:
 p=PRIVATE/'issue-create.stderr';p.write_bytes(r.stderr);p.chmod(0o600)
result={'observed_at':now(),'exit_code':r.returncode,'stdout_sha256':h(r.stdout),'stderr_sha256':h(r.stderr),'raw_stderr_public':False}
url=r.stdout.decode(errors='replace').strip()
match=re.fullmatch(r'https://github\.com/WuKongIM/WuKongIM/issues/([0-9]+)',url)
if r.returncode==0 and match:result['url']=url;result['number']=int(match.group(1))
save('issue-create-result.json',result)
require(r.returncode==0 and match is not None,'Issue create outcome must be read-reconciled before any other mutation')
issue=get('issue-exact-after-create','repos/WuKongIM/WuKongIM/issues/'+match.group(1))
require(issue['title']==title and issue['state']=='open' and issue['locked'] is False and 'pull_request' not in issue and issue['repository_url']=='https://api.github.com/repos/WuKongIM/WuKongIM' and isinstance(issue['comments'],int),'created Issue identity incomplete')
require(issue['body'].rstrip()==(D/'issue-body.md').read_text().rstrip(),'created Issue body mismatch')
search=get('issue-search-after-create','search/issues',[('q',query),('per_page','100')])
require(search['incomplete_results'] is False and search['total_count']==len(search['items'])==1 and search['items'][0]['id']==issue['id'] and search['items'][0]['title']==title,'exact Issue search not yet unique; read reconcile required')
main2=get('main-post-issue-create','repos/WuKongIM/WuKongIM/git/ref/heads/main');require(main2['object']['sha']==C,'remote main changed after Issue creation')
save('issue-verification.json',{'observed_at':now(),'request_id':request,'title':title,'id':issue['id'],'number':issue['number'],'url':issue['html_url'],'state':issue['state'],'locked':issue['locked'],'comments':issue['comments'],'repository_url':issue['repository_url'],'exact_search_count':1,'search_complete':True,'body_matches':True,'main_exact':True,'verification':'official gh GET and exact-title complete Search; not execution of the environment-token preflight shell','issue_create_attempts':1,'dispatched':False})
save('planned-dispatch.json',{'schema':'wukongim.cloud_rc.planned_dispatch/v1','prepared_at':now(),'executed':False,'dispatch_owner':'/root/cloud_rc_execute','repository':'WuKongIM/WuKongIM','workflow':'.github/workflows/chat-lifecycle-rehearsal.yml','ref':'main','source_sha':C,'control_sha':C,'inputs_file':'planned-workflow-inputs.json','inputs_sha256':h((D/'planned-workflow-inputs.json').read_bytes()),'input_count':6,'request_id':request,'tracking_issue':issue['html_url'],'prior_quote_reserve_micros':223432800,'request_budget_limit_micros':'1276567200','operational_stop_micros':1126567200,'provider_calls':False,'workflow_or_permission_changes':False,'next_action':'sole executor /root/cloud_rc_execute fresh exact main guard and single authorized dispatch'})
print(json.dumps({'request_id':request,'issue':issue['html_url'],'main':C,'inputs':str(D/'planned-workflow-inputs.json'),'dispatched':False}))
