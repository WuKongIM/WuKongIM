"""One bounded GET-only snapshot for the exact analysis correlation."""
from pathlib import Path
import sys,subprocess,json,hashlib,datetime
b=Path(__file__).parent;p=b.parent.parent/'rc-new-cee5-private'/b.name
d=b/sys.argv[1];d.mkdir(exist_ok=False)
def get(name,endpoint):
 r=subprocess.run(['gh','api',endpoint],capture_output=True,timeout=45)
 f=p/(sys.argv[1]+'-'+name+'.stderr');f.write_bytes(r.stderr);f.chmod(0o600)
 (d/(name+'-read.json')).write_text(json.dumps({'method':'GET','endpoint':endpoint,'exit_code':r.returncode,'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'bytes':len(r.stdout),'sha256':hashlib.sha256(r.stdout).hexdigest(),'stderr_bytes':len(r.stderr),'stderr_sha256':hashlib.sha256(r.stderr).hexdigest()},indent=2)+'\n')
 if r.returncode:raise SystemExit('GET failed '+name)
 return json.loads(r.stdout)
a=get('discovery','repos/WuKongIM/WuKongIM/actions/workflows/cloud-lease-analyze.yml/runs?per_page=20')
runs=[r for r in a['workflow_runs'] if r['display_title'].endswith('local-fhfYnP-70453')]
(d/'runs.json').write_text(json.dumps({'workflow_runs':runs,'selection':'exact correlation from newest20'},indent=2)+'\n')
for r in runs:
 if 'inspect ' in r['display_title']:continue
 for kind,suffix in [('jobs','/jobs?filter=all&per_page=100'),('artifacts','/artifacts?per_page=100')]:
  name=str(r['id'])+'-'+kind; obj=get(name,'repos/WuKongIM/WuKongIM/actions/runs/'+str(r['id'])+suffix);(d/(name+'.json')).write_text(json.dumps(obj,indent=2)+'\n')
 print(r['id'],r['display_title'],r['status'],r['conclusion'])
