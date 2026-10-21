#!/usr/bin/env python3
import argparse,datetime,hashlib,json,os,re,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--artifact-id',type=int,required=True);p.add_argument('--output',type=Path,required=True);p.add_argument('--receipt',type=Path,required=True);a=p.parse_args()
now=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat();start=now()
os.umask(0o077)
with a.output.open('xb') as f:r=subprocess.run(['gh','api','--method','GET',f'repos/WuKongIM/WuKongIM/actions/artifacts/{a.artifact_id}/zip'],stdout=f,stderr=subprocess.PIPE,timeout=180)
stderr_path=a.output.with_suffix('.stderr');stderr_path.write_bytes(r.stderr);stderr_path.chmod(0o600)
safe=re.sub(r'https?://[^\s\"]+', '<url-redacted>',r.stderr.decode(errors='replace'))
receipt={'artifact_id':a.artifact_id,'started_at':start,'finished_at':now(),'exit_code':r.returncode,'downloaded_bytes':a.output.stat().st_size,'stderr_bytes':len(r.stderr),'stderr_sha256':hashlib.sha256(r.stderr).hexdigest(),'stderr_redacted':safe,'private_urls_not_in_public_receipt':True,'raw_stderr_retained_private':True}
with a.receipt.open('x') as f:json.dump(receipt,f,indent=2);f.write('\n')
print(json.dumps(receipt));raise SystemExit(r.returncode)
