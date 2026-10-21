#!/usr/bin/env python3
"""One bounded GET-only snapshot of the exact request and its safety finalizer."""
import argparse,concurrent.futures,datetime,hashlib,json,re,subprocess,time
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--output',type=Path,required=True);a=p.parse_args();a.output.mkdir(parents=True)
ROOT=Path('/tmp/wukongim-v3-readiness-20261021/cloud/rc-new-cee5');PRIVATE=ROOT.parent/'rc-new-cee5-private/remote-progress';PRIVATE.mkdir(mode=0o700,exist_ok=True)
REQ='chat-20261021T163850Z-4ccd7506';PREFIX='repos/WuKongIM/WuKongIM/';NOW=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat()
def save(name,x):(a.output/name).write_text(json.dumps(x,ensure_ascii=False,indent=2)+'\n')
def get(path):
 for attempt in (1,2):
  r=subprocess.run(['gh','api','--method','GET',PREFIX+path],capture_output=True,timeout=55)
  if r.returncode==0:return json.loads(r.stdout)
  key=a.output.name+'-'+hashlib.sha256(path.encode()).hexdigest()[:12]+'-'+str(attempt);f=PRIVATE/(key+'.stderr');f.write_bytes(r.stderr);f.chmod(0o600)
  save('read-failure-'+key+'.json',{'endpoint':path,'attempt':attempt,'exit_code':r.returncode,'stderr_sha256':hashlib.sha256(r.stderr).hexdigest(),'stderr_redacted':re.sub(r'https?://[^\s\"]+','<url-redacted>',r.stderr.decode(errors='replace')),'observed_at':NOW()})
  if attempt==1 and any(x in r.stderr for x in (b'EOF',b'TLS handshake timeout',b'connection reset')):time.sleep(2);continue
  raise RuntimeError('GET failed: '+path)
paths=['issues/1023','issues/1023/comments?per_page=100','actions/workflows/chat-lifecycle-rehearsal-finalize.yml','actions/workflows/chat-lifecycle-rehearsal-finalize.yml/runs?per_page=100&created=%3E%3D2026-10-21T17%3A05%3A48Z']
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:issue,comments,wf,listed=list(pool.map(get,paths))
assert issue['number']==1023 and issue['title']=='[chat-lifecycle] '+REQ
assert wf['id']==329938002 and wf['path']=='.github/workflows/chat-lifecycle-rehearsal-finalize.yml'
safe_issue={k:issue.get(k) for k in ['number','title','state','locked','updated_at','comments','html_url']};save('issue.json',safe_issue)
safe_comments=[{k:c.get(k) for k in ['id','created_at','updated_at','body','html_url']}|{'author':c.get('user',{}).get('login')} for c in comments];save('comments.json',safe_comments)
safe_wf={k:wf.get(k) for k in ['id','name','path','state','created_at','updated_at','html_url']};save('workflow.json',safe_wf)
fields=['id','run_number','run_attempt','event','display_title','path','head_sha','head_branch','status','conclusion','created_at','updated_at','html_url'];runs=[{k:r.get(k) for k in fields} for r in listed['workflow_runs']];save('finalizers.json',{'total_count':listed['total_count'],'complete_page':listed['total_count']<=len(runs),'runs':runs})
relevant=[]
for run in runs[:10]:
 jobs=get('actions/runs/'+str(run['id'])+'/jobs?per_page=100');save(str(run['id'])+'-jobs.json',jobs)
 exact=[j for j in jobs['jobs'] if REQ in j['name']]
 if exact:
  artifacts=get('actions/runs/'+str(run['id'])+'/artifacts?per_page=100');save(str(run['id'])+'-artifacts.json',artifacts)
  relevant.append({'run':run,'jobs':[{'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'started_at':j['started_at'],'completed_at':j['completed_at'],'steps':[{'name':s['name'],'status':s['status'],'conclusion':s['conclusion']} for s in j['steps']]} for j in exact],'artifacts':[{'id':x['id'],'name':x['name'],'size_in_bytes':x['size_in_bytes'],'digest':x.get('digest')} for x in artifacts['artifacts'] if REQ in x['name']]})
s={'observed_at':NOW(),'request_id':REQ,'issue':safe_issue,'latest_issue_comment':safe_comments[-1] if safe_comments else None,'workflow':safe_wf,'runs_since_measured_start':len(runs),'total_count':listed['total_count'],'all_listed_runs_job_checked':len(runs)<=10,'relevant_finalizers':relevant,'remote_runtime_health_independently_known':False,'two_hour_result':'not_observed_without_terminal_artifact','no_schedule_entry_does_not_prove_schedule_fault':True};save('summary.json',s)
print(json.dumps({'observed_at':s['observed_at'],'workflow_state':wf['state'],'issue_state':issue['state'],'issue_comments':issue['comments'],'latest_comment_at':safe_comments[-1]['created_at'] if safe_comments else None,'runs_since_start':len(runs),'relevant_finalizers':relevant},ensure_ascii=False))
