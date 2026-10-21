from pathlib import Path
import subprocess,json,hashlib,datetime,os,re,sys,gzip
P=Path(__file__).resolve().parent
D=P.parent/'pr78-main-first-ci-private'
def sha(b):return hashlib.sha256(b).hexdigest()
def get(endpoint,name,cap=64*1024*1024):
 path=P/name;path.parent.mkdir(parents=True,exist_ok=True);assert not path.exists(),name
 stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ');raw=D/(stamp+'-'+path.name);err=raw.with_suffix(raw.suffix+'.stderr')
 with raw.open('xb') as out,err.open('xb') as e:
  os.chmod(raw,0o600);os.chmod(err,0o600)
  try:r=subprocess.run(['gh','api','--method','GET',endpoint],stdout=out,stderr=e,timeout=45);code=r.returncode
  except subprocess.TimeoutExpired:code=124
 b=raw.read_bytes();eb=err.read_bytes();rec={'endpoint':endpoint,'started_at':stamp,'exit_code':code,'raw_bytes':len(b),'raw_sha256':sha(b),'stderr_bytes':len(eb),'stderr_sha256':sha(eb)}
 if code==0:
  assert len(b)<=cap
  text=b.decode('utf-8')
  text=re.sub(r'(https?://[^\s\"<>?]+)\?[^\s\"<>]*',r'\1?[query-redacted]',text)
  text=re.sub(r'github_pat_[A-Za-z0-9_]+|gh[pousr]_[A-Za-z0-9]{30,}','[token-redacted]',text)
  assert not re.search(r'-----BEGIN (?:OPENSSH |RSA |EC |DSA )?PRIVATE KEY',text)
  public=text.encode();path.write_bytes(gzip.compress(public,mtime=0) if name.endswith('.gz') else public)
  rec.update(public_bytes=len(public),public_sha256=sha(public),redacted=public!=b)
 else:rec['public_error_class']='GET_failed'
 (P/(name+'.read.json')).write_text(json.dumps(rec,indent=2)+'\n')
 print(name,code,len(b),flush=True)
 return rec
if __name__=='__main__':get(sys.argv[1],sys.argv[2])
