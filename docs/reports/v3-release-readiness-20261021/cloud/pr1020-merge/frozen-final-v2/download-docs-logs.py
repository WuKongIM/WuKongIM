from pathlib import Path
import hashlib,json,re,subprocess,sys,datetime
p=Path(__file__).resolve().parent;out=p/'postmerge-logs-public';out.mkdir(exist_ok=True);raw=p/'postmerge-logs-private';raw.mkdir(mode=0o700,exist_ok=True)
s=json.loads((p/sys.argv[1]/'summary.json').read_text());assert len(s['runs'])==1;r=s['runs'][0];assert r['id']==39601918104 and r['status']=='completed'
for j in r['jobs']:
 if j['conclusion']=='skipped':continue
 args=['gh','api','--method','GET',f"repos/WuKongIM/WuKongIM/actions/jobs/{j['id']}/logs"]
 f=raw/(str(j['id'])+'.log')
 if f.exists():b=f.read_bytes()
 else:
  result=subprocess.run(args,capture_output=True,timeout=90);assert result.returncode==0,'read-only public job-log GET failed';b=result.stdout;assert len(b)<=16*1024*1024;f.write_bytes(b);f.chmod(0o600)
 lines=[];red=[]
 for n,line in enumerate(b.decode().splitlines(keepends=True),1):
  if re.search(r'token|password|authorization|private.?key|secret',line,re.I):red.append({'line':n,'bytes':len(line.encode()),'sha256':hashlib.sha256(line.encode()).hexdigest()});lines.append('[redacted authentication-related line; original line hash retained]\n')
  else:lines.append(line)
 public=''.join(lines).encode();assert not re.search(rb'-----BEGIN (?:OPENSSH|RSA|EC|DSA)? ?PRIVATE KEY',public);assert not re.search(rb'(?:github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,})',public)
 (out/f"{j['id']}.log").write_bytes(public)
 receipt={'run_id':r['id'],'job_id':j['id'],'job_name':j['name'],'conclusion':j['conclusion'],'raw_bytes':len(b),'raw_sha256':hashlib.sha256(b).hexdigest(),'public_sha256':hashlib.sha256(public).hexdigest(),'exact_merge_sha_present':'e30299fa59ea6669ea71024eabe209840f241f19' in b.decode(),'redacted_lines':red}
 (out/f"{j['id']}.json").write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps({k:receipt[k] for k in ['job_id','conclusion','raw_bytes','exact_merge_sha_present']}),flush=True)
