from pathlib import Path
import subprocess,json,hashlib,datetime,os,sys
out=Path(__file__).parent; private=out.parent/'candidate-private';n=sys.argv[1]; dest=out/('snapshot-'+n); dest.mkdir(exist_ok=False)
reads={'run':'actions/runs/39664872167','jobs':'actions/runs/39664872167/jobs?per_page=100','artifacts':'actions/runs/39664872167/artifacts?per_page=100','pr':'pulls/1024'}
for name,endpoint in reads.items():
 with (private/(n+'-'+name+'.stderr')).open('xb') as e:
  os.chmod(e.name,0o600)
  r=subprocess.run(['gh','api','repos/WuKongIM/WuKongIM/'+endpoint],stdout=subprocess.PIPE,stderr=e,timeout=40)
 error=(private/(n+'-'+name+'.stderr')).read_bytes()
 receipt={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'endpoint':endpoint,'exit_code':r.returncode,'stderr_sha256':hashlib.sha256(error).hexdigest(),'stderr_bytes':len(error)}
 if r.returncode==0:
  v=json.loads(r.stdout);(dest/(name+'.json')).write_bytes(r.stdout)
 else: receipt['error_class']='GET_failed'
 (dest/(name+'-read.json')).write_text(json.dumps(receipt,indent=2)+'\n')
 if name=='run' and r.returncode==0:print(json.dumps({k:v[k] for k in ['id','head_sha','run_attempt','status','conclusion']}),flush=True)
 if name=='jobs' and r.returncode==0:print(json.dumps([{'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'started_at':j['started_at'],'steps':[{k:x.get(k) for k in ['name','status','conclusion','started_at','completed_at']} for x in j['steps']]} for j in v['jobs']]),flush=True)
 if name=='artifacts' and r.returncode==0:print(json.dumps([{k:a[k] for k in ['id','name','size_in_bytes','digest']} for a in v['artifacts']]),flush=True)
(out/'latest-snapshot.txt').write_text(dest.name+'\n')
