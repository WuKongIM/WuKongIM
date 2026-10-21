"""Bounded, GET-only exact Stop/finalizer/Release observation."""
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
import subprocess,json,datetime,hashlib,sys
b=Path(__file__).parent;d=b/sys.argv[1];d.mkdir(exist_ok=False)
p=b.parents[2]/'rc-new-cee5-private'/'stop-release-observation';p.mkdir(parents=True,mode=0o700,exist_ok=True)
REQ='chat-20261021T163850Z-4ccd7506';prefix='repos/WuKongIM/WuKongIM/'
def get(name,path):
 r=subprocess.run(['gh','api',prefix+path],capture_output=True,timeout=50)
 f=p/(sys.argv[1]+'-'+name+'.stderr');f.write_bytes(r.stderr);f.chmod(0o600)
 (d/(name+'-read.json')).write_text(json.dumps({'method':'GET','endpoint':prefix+path,'exit_code':r.returncode,'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'bytes':len(r.stdout),'sha256':hashlib.sha256(r.stdout).hexdigest(),'stderr_sha256':hashlib.sha256(r.stderr).hexdigest()},indent=2)+'\n')
 if r.returncode:raise RuntimeError('GETfailed '+name)
 return json.loads(r.stdout)
def query(item):
 name,path=item;x=get(name,path);(d/(name+'.json')).write_text(json.dumps(x,indent=2)+'\n');return name,x
queries=[('stop','actions/runs/39645737564'),('comments','issues/1023/comments?per_page=100')]+[(wf,'actions/workflows/'+wf+'.yml/runs?per_page=20&created=%3E%3D2026-10-21T17%3A39%3A20Z') for wf in ['chat-lifecycle-rehearsal-finalize','chat-lifecycle-formal-finalize','cloud-lease-release']]
with ThreadPoolExecutor(max_workers=5) as pool: results=dict(pool.map(query,queries))
runs=[results['stop']]
for wf in ['chat-lifecycle-rehearsal-finalize','chat-lifecycle-formal-finalize','cloud-lease-release']:
 runs += [r for r in results[wf]['workflow_runs'] if REQ in r['display_title']]
for r in runs:
 for name,suffix in [('jobs','/jobs?filter=all&per_page=100'),('artifacts','/artifacts?per_page=100')]:
  obj=get(str(r['id'])+'-'+name,'actions/runs/'+str(r['id'])+suffix);(d/(str(r['id'])+'-'+name+'.json')).write_text(json.dumps(obj,indent=2)+'\n')
 print(r['id'],r['display_title'],r['head_sha'][:7],r['status'],r['conclusion'])
print('latestcomment',results['comments'][-1]['body'] if results['comments'] else None)
