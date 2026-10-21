from pathlib import Path,PurePosixPath
import subprocess,json,hashlib,datetime,os,re,zipfile,stat,sys
out=Path(__file__).resolve().parent;private=out.parent/'postmerge-ci-agent-private';snap=out/(out/'latest-snapshot.txt').read_text().strip()
head='cee5d51f325fb84033060f546337d326110adae5';merge='cee5d51f325fb84033060f546337d326110adae5'
redact=re.compile(r'token|password|authorization|private.?key|secret|acs:ram::[0-9]+:|[?&](?:sig|signature|token)=',re.I)
def sha(b):return hashlib.sha256(b).hexdigest()
def public_text(raw):
 lines=[];removed=[]
 for n,line in enumerate(raw.decode('utf-8').splitlines(keepends=True),1):
  if redact.search(line):removed.append({'line':n,'size':len(line.encode()),'sha256':sha(line.encode())});lines.append('[redacted authentication-related line; original hash retained]\n')
  else:lines.append(line)
 b=''.join(lines).encode();assert not re.search(rb'-----BEGIN (?:OPENSSH|RSA|EC|DSA)? ?PRIVATE KEY|github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,}',b)
 return b,removed
def download(endpoint,path,cap):
 err=path.with_suffix(path.suffix+'.stderr');assert not path.exists()
 with path.open('xb') as stdout,err.open('xb') as stderr:
  os.chmod(path,0o600);os.chmod(err,0o600)
  r=subprocess.run(['gh','api',endpoint],stdout=stdout,stderr=stderr,timeout=180)
 e=err.read_bytes();receipt={'endpoint':endpoint,'exit_code':r.returncode,'raw_stderr_sha256':sha(e),'raw_stderr_bytes':len(e),'error_class':None if r.returncode==0 else 'GET_failed','at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
 if r.returncode!=0:
  receipt['public_stderr']=re.sub(r'https://[^\s\"\']+','<redacted-url>',e.decode(errors='replace'))[:2000]
  return None,receipt
 assert path.stat().st_size<=cap
 return path.read_bytes(),receipt
logs=out/'job-logs-public';logs.mkdir(exist_ok=True);arts=out/'artifacts-public';arts.mkdir(exist_ok=True)
for f in sorted(snap.glob('*-jobs.json')):
 for job in json.loads(f.read_text())['jobs']:
  if job['status']!='completed':continue
  assert job['run_attempt']==1 and job['head_sha'] in [head,merge]
  dest=logs/str(job['id']);dest.mkdir(exist_ok=True)
  (dest/'job.json').write_text(json.dumps(job,indent=2)+'\n')
  if job['conclusion']=='skipped' or (dest/'receipt.json').exists():continue
  rawpath=private/(str(job['id'])+'.log')
  if rawpath.exists():continue # Preserve first failed GET; explicit subsequent decision required.
  raw,rec=download(f"repos/WuKongIM/WuKongIM/actions/jobs/{job['id']}/logs",rawpath,32*1024**2)
  if raw is None:(dest/'first-read-failure.json').write_text(json.dumps(rec,indent=2)+'\n');print('JOB GET FAILED',job['id'],flush=True);continue
  b,removed=public_text(raw);(dest/'complete.log').write_bytes(b)
  rec.update(job_id=job['id'],run_id=job['run_id'],conclusion=job['conclusion'],raw_bytes=len(raw),raw_sha256=sha(raw),public_bytes=len(b),public_sha256=sha(b),redacted_lines=removed,synthetic_sha_present=merge.encode() in raw,head_sha_present=head.encode() in raw)
  (dest/'receipt.json').write_text(json.dumps(rec,indent=2)+'\n');print('JOB',job['id'],job['conclusion'],len(raw),flush=True)
