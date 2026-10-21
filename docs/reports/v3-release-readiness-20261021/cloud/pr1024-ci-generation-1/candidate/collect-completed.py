from pathlib import Path,PurePosixPath
import subprocess,json,hashlib,datetime,os,re,zipfile,stat,sys
out=Path(__file__).resolve().parent;private=out.parent.parent/'pr1024-ci-generation-1-private';snap=out/(out/'latest-snapshot.txt').read_text().strip()
head='c91e37b4e35e1009c9c5cb55ff351a38b870014f';merge='4f9e71ac3976fab087833bb230c391cd5ea5f46e'
redact=re.compile(r'token|password|authorization|private.?key|secret|[?&](?:sig|signature|token)=',re.I)
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
for f in sorted(snap.glob('jobs.json')):
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
for f in sorted(snap.glob('artifacts.json')):
 for a in json.loads(f.read_text())['artifacts']:
  dest=arts/str(a['id']);dest.mkdir(exist_ok=True)
  if (dest/'receipt.json').exists():continue
  assert not a['expired'] and a['workflow_run']['head_sha']==head and a['workflow_run']['repository_id']==638054601 and a['workflow_run']['head_repository_id']==638054601
  if a['size_in_bytes']>64*1024**2:
   (dest/'not-downloaded.json').write_text(json.dumps({'artifact':a,'reason':'exceeds full-download bound; requires selected-member decision'},indent=2)+'\n');print('ARTIFACT SIZE BOUND',a['id'],a['size_in_bytes'],flush=True);continue
  rawpath=private/(str(a['id'])+'.zip')
  if rawpath.exists():continue
  raw,rec=download(f"repos/WuKongIM/WuKongIM/actions/artifacts/{a['id']}/zip",rawpath,64*1024**2)
  if raw is None:(dest/'first-read-failure.json').write_text(json.dumps(rec,indent=2)+'\n');print('ARTIFACT GET FAILED',a['id'],flush=True);continue
  assert len(raw)==a['size_in_bytes'] and a['digest']=='sha256:'+sha(raw)
  inventory=[];testevents=[]
  with zipfile.ZipFile(rawpath) as z:
   infos=z.infolist();assert len(infos)<10000 and len({i.filename for i in infos})==len(infos) and sum(i.file_size for i in infos)<512*1024**2
   assert z.testzip() is None
   for i in infos:
    path=PurePosixPath(i.filename);assert not path.is_absolute() and '..' not in path.parts and not stat.S_ISLNK(i.external_attr>>16)
    row={'path':i.filename,'size':i.file_size,'compressed_size':i.compress_size,'crc32':format(i.CRC,'08x'),'retained':False}
    if not i.is_dir() and path.suffix in ['.json','.jsonl','.log','.txt','.out','.stderr','.sha256'] and i.file_size<=64*1024**2:
     content=z.read(i);row['sha256']=sha(content)
     try:content.decode('utf-8')
     except UnicodeDecodeError:inventory.append(row);continue
     for line in content.splitlines():
      try:event=json.loads(line)
      except (json.JSONDecodeError,UnicodeDecodeError):continue
      if isinstance(event,dict) and event.get('Action') in ['run','pass','fail','skip'] and 'Test' in event:
       testevents.append({k:event[k] for k in ['Time','Action','Package','Test','Elapsed'] if k in event})
     # Structured JSON receipts have no credential environment. Preserve their schema.
     if path.suffix=='.json':
      try:json.loads(content);structured=True
      except json.JSONDecodeError:structured=False
     else:structured=False
     if structured:
      assert not re.search(rb'-----BEGIN (?:OPENSSH|RSA|EC|DSA)? ?PRIVATE KEY|github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,}',content)
      public=content;removed=[]
     else:public,removed=public_text(content)
     target=dest/'members'/path;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(public)
     row.update(retained=True,public_sha256=sha(public),public_bytes=len(public),redacted_lines=removed)
    inventory.append(row)
  (dest/'inventory.json').write_text(json.dumps(inventory,indent=2)+'\n')
  (dest/'test-events.json').write_text(json.dumps(testevents,indent=2)+'\n')
  rec.update(artifact=a,observed_bytes=len(raw),observed_sha256=sha(raw),zip_crc_pass=True,member_count=len(inventory),retained_count=sum(i['retained'] for i in inventory))
  (dest/'receipt.json').write_text(json.dumps(rec,indent=2)+'\n');print('ARTIFACT',a['id'],a['name'],len(raw),'members',len(inventory),flush=True)
