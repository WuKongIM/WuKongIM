#!/usr/bin/env python3
import subprocess,hashlib,json,re,datetime
from pathlib import Path
out=Path(__file__).parent
r=subprocess.run(['gh','run','view','39581355444','--repo','WuKongIM/WuKongIM','--log'],capture_output=True,timeout=120)
raw=r.stdout; text=raw.decode(errors='replace'); lines=text.splitlines(); result=[]; blocked=False
for line in lines:
 if re.search(r'-----BEGIN .*PRIVATE KEY',line): blocked=True
 if blocked:
  if re.search(r'-----END .*PRIVATE KEY',line): blocked=False
  result.append('[PRIVATE KEY CONTENT REDACTED]');continue
 # Entire environment/assignment lines with credentials are omitted, not partially masked.
 if re.search(r'(?i)(token|secret|password|authorization|credential|private.key|private_key|access_key|accesskey)\s*[:=]',line):
  result.append('[CREDENTIAL ASSIGNMENT REDACTED]');continue
 line=re.sub(r'https?://[^\s<>"\']+[?][^\s<>"\']+', '<url-query-redacted>', line)
 line=re.sub(r'(?i)Bearer\s+[^\s]+','Bearer <redacted>',line)
 line=re.sub(r'\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]+\b','<github-token-redacted>',line)
 line=re.sub(r'/[^\s"\']*(?:credentials|private-key|diagnostic_ed25519|manager-token)[^\s"\']*','<sensitive-path-redacted>',line,flags=re.I)
 result.append(line)
safe='\n'.join(result)+'\n'
(out/'top-log.redacted.txt').write_text(safe)
(out/'top-log-retrieval.json').write_text(json.dumps({'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'run_id':39581355444,'exit_code':r.returncode,'raw_bytes':len(raw),'raw_sha256':hashlib.sha256(raw).hexdigest(),'raw_not_stored':True,'redacted_bytes':len(safe.encode()),'redacted_sha256':hashlib.sha256(safe.encode()).hexdigest(),'stderr_bytes':len(r.stderr),'stderr_sha256':hashlib.sha256(r.stderr).hexdigest()},indent=2)+'\n')
print(json.dumps({'exit_code':r.returncode,'raw_bytes':len(raw),'redacted_lines':len(result)}))
raise SystemExit(r.returncode)
