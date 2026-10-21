#!/usr/bin/env python3
import argparse,concurrent.futures,datetime,hashlib,json,re,subprocess,time,urllib.parse
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--output',type=Path,required=True);a=p.parse_args();a.output.mkdir()
request='chat-20261021T163850Z-4ccd7506'; repo='repos/WuKongIM/WuKongIM';top=39637426411
private=Path('/tmp/wukongim-v3-readiness-20261021/cloud/rc-new-cee5-private/live-observation');private.mkdir(mode=0o700,exist_ok=True)
fields=['id','run_number','workflow_id','run_attempt','status','conclusion','head_sha','head_branch','display_title','path','created_at','updated_at','html_url','event']
def get(path):
 for attempt in (1,2):
  r=subprocess.run(['gh','api','--method','GET',path,'-H','X-GitHub-Api-Version: 2026-03-10'],capture_output=True,timeout=60)
  if r.returncode==0:return json.loads(r.stdout)
  private_error=private/(a.output.name+'-'+hashlib.sha256(path.encode()).hexdigest()[:10]+'-'+str(attempt)+'.stderr');private_error.write_bytes(r.stderr);private_error.chmod(0o600)
  failure={'endpoint':path,'attempt':attempt,'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'exit_code':r.returncode,'stderr_sha256':hashlib.sha256(r.stderr).hexdigest(),'stderr_redacted':re.sub(r'https?://[^\s\"]+', '<url-redacted>', r.stderr.decode(errors='replace'))}
  (a.output/('get-failure-'+hashlib.sha256(path.encode()).hexdigest()[:10]+'-'+str(attempt)+'.json')).write_text(json.dumps(failure,indent=2)+'\n')
  if attempt==1 and any(term in r.stderr for term in (b'EOF',b'TLS handshake timeout',b'connection reset')):time.sleep(2);continue
  raise RuntimeError('GET failed '+path)
 raise RuntimeError('GET retries exhausted '+path)
def save(name,data):(a.output/name).write_text(json.dumps(data,ensure_ascii=False,indent=2)+'\n')
paths=[repo+f'/actions/runs/{top}',repo+f'/actions/runs/{top}/jobs?per_page=100',repo+'/actions/runs?per_page=100&created='+urllib.parse.quote('>=2026-10-21T16:44:07Z'),repo+f'/actions/runs/{top}/artifacts?per_page=100']
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:d=list(pool.map(get,paths))
t={k:d[0].get(k) for k in fields};jobs=d[1];runs=d[2];artifacts=d[3]
assert t['head_sha']=='cee5d51f325fb84033060f546337d326110adae5' and t['run_attempt']==1 and t['run_number']==34 and t['workflow_id']==329938003
matched=[{k:r.get(k) for k in fields} for r in runs['workflow_runs'] if request in r['display_title']]
save('top.json',t);save('top-jobs.json',jobs);save('request-runs.json',{'total_count':runs['total_count'],'count_returned':len(runs['workflow_runs']),'complete_page':runs['total_count']<=len(runs['workflow_runs']),'matches':matched});save('top-artifacts.json',artifacts)
children=[]
for r in matched:
 if r['id']==top:continue
 cached=None
 if r['status']=='completed':
  for prior in sorted(a.output.parent.glob('poll-*'),reverse=True):
   if prior==a.output or not prior.is_dir():continue
   rf=prior/'request-runs.json';jf=prior/(str(r['id'])+'-jobs.json');af=prior/(str(r['id'])+'-artifacts.json')
   if not (rf.exists() and jf.exists() and af.exists()):continue
   previous=json.loads(rf.read_text())['matches']
   if any(all(old.get(k)==r.get(k) for k in ['id','run_attempt','status','conclusion','head_sha']) for old in previous):
    cached=prior;j=json.loads(jf.read_text());ar=json.loads(af.read_text());break
 if cached is None:
  j=get(repo+f"/actions/runs/{r['id']}/jobs?per_page=100");ar=get(repo+f"/actions/runs/{r['id']}/artifacts?per_page=100")
 else:save(str(r['id'])+'-cache-basis.json',{'prior_poll':str(cached),'reason':'completed same attempt/head/conclusion in current run list; no new metadata claim'})
 save(str(r['id'])+'-jobs.json',j);save(str(r['id'])+'-artifacts.json',ar)
 children.append(r|{'current_steps':[{'job':x['name'],'step':s['name']} for x in j['jobs'] for s in x['steps'] if s['status']=='in_progress'],'artifacts':[{'id':x['id'],'name':x['name'],'size_in_bytes':x['size_in_bytes'],'digest':x.get('digest')} for x in ar['artifacts']]})
summary={'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'top':t,'top_steps':[{'name':s['name'],'status':s['status'],'conclusion':s['conclusion']} for x in jobs['jobs'] for s in x['steps']],'children':children,'artifacts':artifacts['artifacts']};save('summary.json',summary);print(json.dumps({'observed_at':summary['observed_at'],'top':summary['top'],'active_top_steps':[s for s in summary['top_steps'] if s['status']=='in_progress' or s['conclusion'] in ('failure','cancelled')],'children':[{'id':x['id'],'path':x['path'],'status':x['status'],'conclusion':x['conclusion'],'head_sha':x['head_sha'],'current_steps':x['current_steps'],'artifacts':x['artifacts']} for x in summary['children']],'artifact_count':len(summary['artifacts'])},ensure_ascii=False))
