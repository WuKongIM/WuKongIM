#!/usr/bin/env python3
import argparse,datetime,hashlib,json,os,re,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--job',type=int,required=True);a=p.parse_args()
D=Path(__file__).parent;PRIVATE=D.parent.parent/'pr1024-authorized-run-resume-20261023-private/live-observation';os.umask(0o077)
out=D/'job-checkouts';out.mkdir(exist_ok=True)
rawfile=PRIVATE/(str(a.job)+'.log');errorfile=PRIVATE/(str(a.job)+'.stderr');start=datetime.datetime.now(datetime.timezone.utc).isoformat()
with rawfile.open('xb') as f:
 r=subprocess.run(['gh','api','--method','GET',f'repos/WuKongIM/WuKongIM/actions/jobs/{a.job}/logs'],stdout=f,stderr=subprocess.PIPE,timeout=60)
errorfile.write_bytes(r.stderr);errorfile.chmod(0o600)
b=rawfile.read_bytes();assert len(b)<=16*1024*1024,'unexpected large job log'
rec={'job_id':a.job,'started_at':start,'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'exit_code':r.returncode,'raw_log_bytes':len(b),'raw_log_sha256':hashlib.sha256(b).hexdigest(),'stderr_bytes':len(r.stderr),'stderr_sha256':hashlib.sha256(r.stderr).hexdigest(),'raw_files_private':True}
if r.returncode==0:
 values=[];lines=b.decode(errors='replace').splitlines()
 for index,line in enumerate(lines):
  m=re.fullmatch(r'\S+Z\s+([a-f0-9]{40})\s*',line)
  if m and any('log -1 --format=%H' in x for x in lines[max(0,index-3):index]):values.append({'line':index+1,'commit':m[1]})
 rec['actual_checkout_commits']=values;rec['all_observed_checkouts_exact']=bool(values) and all(x['commit']=='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06' for x in values)
 rec['limits']='Only bounded commit output after git log -1 --format=%H is copied publicly. Full raw log retained privately; no arbitrary command/environment contents copied.'
(out/(str(a.job)+'.json')).write_text(json.dumps(rec,indent=2)+'\n');print(json.dumps(rec));raise SystemExit(r.returncode)
