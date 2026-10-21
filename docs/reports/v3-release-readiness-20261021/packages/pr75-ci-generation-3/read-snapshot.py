from pathlib import Path
import subprocess,concurrent.futures,json,hashlib,datetime,os,sys
out=Path(__file__).resolve().parent;private=out.parent/'pr75-ci-generation-3-private';stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');d=out/'snapshots'/stamp;d.mkdir(parents=True)
runs=[int(x) for x in sys.argv[1:]] if len(sys.argv)>1 else [39641288308, 39641288253]
queries={f'{r}-{part}':f'repos/WuKongIM/packages/actions/runs/{r}'+('' if part=='run' else '/'+part+'?per_page=100') for r in runs for part in ['run','jobs','artifacts']}
def get(item):
 name,endpoint=item;r=subprocess.run(['gh','api',endpoint],capture_output=True,timeout=45);err=private/(stamp+'-'+name+'.stderr');err.write_bytes(r.stderr);os.chmod(err,0o600);rec={'endpoint':endpoint,'exit_code':r.returncode,'stderr_bytes':len(r.stderr),'stderr_sha256':hashlib.sha256(r.stderr).hexdigest()};value=None
 if r.returncode==0:
  value=json.loads(r.stdout);(d/(name+'.json')).write_bytes(r.stdout);rec['stdout_sha256']=hashlib.sha256(r.stdout).hexdigest()
 (d/(name+'.read.json')).write_text(json.dumps(rec,indent=2)+'\n');return name,value,rec
allresults={}
for name,value,rec in concurrent.futures.ThreadPoolExecutor(max_workers=4).map(get,queries.items()):
 if value is None: print('READ FAILED',name,rec);continue
 allresults[name]=value
 if name.endswith('-run'):print(name,value['status'],value['conclusion'])
 elif name.endswith('-jobs'):print(name,[{'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'active':next((s['name'] for s in j['steps'] if s['status']=='in_progress'),None)} for j in value['jobs']])
 elif name.endswith('-artifacts'):print(name,[{'id':a['id'],'name':a['name'],'size':a['size_in_bytes']} for a in value['artifacts']])
(out/'latest-snapshot.txt').write_text(str(d.relative_to(out))+'\n')
