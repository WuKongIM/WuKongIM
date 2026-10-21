from pathlib import Path
import json,hashlib,subprocess,os,re,datetime
O=Path(__file__).resolve().parent;P=O.with_name(O.name+'-private')
def sha(b):return hashlib.sha256(b).hexdigest()
def get(name,endpoint,log=False):
 rawpath=P/(name+'.raw');error=P/(name+'.stderr')
 with rawpath.open('xb') as f,error.open('xb') as e:
  os.chmod(rawpath,0o600);os.chmod(error,0o600)
  r=subprocess.run(['gh','api','repos/WuKongIM/WuKongIM/'+endpoint],stdout=f,stderr=e,timeout=60)
 raw=rawpath.read_bytes();err=error.read_bytes();rec={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'endpoint':endpoint,'exit_code':r.returncode,'stderr_bytes':len(err),'stderr_sha256':sha(err),'raw_bytes':len(raw),'raw_sha256':sha(raw)}
 if r.returncode==0:
  assert len(raw)<=16*1024**2
  if log:
   redacted=[];lines=[]
   for no,line in enumerate(raw.decode().splitlines(keepends=True),1):
    if re.search(r'token|credential|password|secret|authorization|oidc|bearer|acs:ram::|[?&](sig|signature)=',line,re.I):
     redacted.append({'line':no,'bytes':len(line.encode()),'sha256':sha(line.encode())});lines.append('[redacted identity/authentication line; original hash retained]\n')
    else:lines.append(line)
   public=''.join(lines).encode();rec['redacted_lines']=redacted;rec['public_bytes']=len(public);rec['public_sha256']=sha(public)
   assert not re.search(rb'-----BEGIN (?:OPENSSH|RSA|EC|DSA)? ?PRIVATE KEY-----|github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,}',public)
   (O/(name+'.log')).write_bytes(public)
  else:
   json.loads(raw);(O/(name+'.json')).write_bytes(raw)
 (O/(name+'-read.json')).write_text(json.dumps(rec,indent=2)+'\n')
 print(name,r.returncode,len(raw),flush=True)
 return json.loads(raw) if r.returncode==0 and not log else None
for j in json.loads((O/'docs-jobs.json').read_text())['jobs']:
 assert j['status']=='completed' and j['conclusion']=='success' and j['run_attempt']==1 and j['head_sha']=='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06'
 get('job-'+str(j['id']),'actions/jobs/'+str(j['id'])+'/logs',True)
signal_id=39672951899
run=get('postmerge-signal-run','actions/runs/'+str(signal_id))
assert run['head_sha']=='f419ea8f82c8bd16a777cfc3bf6cbf8c85be60dd' and run['run_attempt']==1 and run['status']=='completed' and run['conclusion']=='success'
jobs=get('postmerge-signal-jobs','actions/runs/'+str(signal_id)+'/jobs?per_page=100')
for j in jobs['jobs']:
 assert j['status']=='completed' and j['conclusion']=='success' and j['run_attempt']==1
 get('job-'+str(j['id']),'actions/jobs/'+str(j['id'])+'/logs',True)
