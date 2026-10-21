from pathlib import Path
import hashlib,importlib.util,json,os,re,sys
sys.dont_write_bytecode=True
root=Path(__file__).resolve().parent
repo=Path("/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-released-continuation")
spec=importlib.util.spec_from_file_location("readonly_api",repo/"scripts/chat-lifecycle/authenticate-released-preclock.py")
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
allowed={r["id"] for r in json.loads((root/"initial-runs.json").read_text())["workflow_runs"]}
rawdir=root/"job-logs-raw";rawdir.mkdir(mode=0o700,exist_ok=True)
out=root/"job-logs-public";out.mkdir(exist_ok=True)
for value in sys.argv[1:]:
 assert value.isdecimal()
 job=m.api("/repos/WuKongIM/WuKongIM/actions/jobs/"+value)
 assert job["run_id"] in allowed and job["run_attempt"]==1 and job["status"]=="completed"
 assert job["head_sha"] in ["0cdfaa37b00c58819895dee54029c27c83c9caa9","922bc66a9cebcbe09bef5fc43afc0593c4f07fb8"]
 (out/(value+"-job.json")).write_text(json.dumps(job,indent=2)+"\n")
 if job["conclusion"]=="skipped":continue
 raw=rawdir/(value+".log")
 if raw.exists():b=raw.read_bytes()
 else:
  old=root/"job-logs"/(value+".log")
  b=old.read_bytes() if old.exists() else m.api("/repos/WuKongIM/WuKongIM/actions/jobs/"+value+"/logs",32*1024*1024,True)
  raw.write_bytes(b);raw.chmod(0o600)
 text=b.decode("utf-8")
 target="922bc66a9cebcbe09bef5fc43afc0593c4f07fb8"
 sha_lines=[x for x in text.splitlines() if target in x or "0cdfaa37b00c58819895dee54029c27c83c9caa9" in x]
 lines=[];redacted=[]
 for index,line in enumerate(text.splitlines(keepends=True),1):
  if re.search(r"token|password|authorization|private.?key|secret",line,re.I):
   redacted.append({"line":index,"sha256":hashlib.sha256(line.encode()).hexdigest(),"bytes":len(line.encode())})
   lines.append("[redacted authentication-related line; original line hash retained]\n")
  else:lines.append(line)
 public="".join(lines).encode()
 assert not re.search(rb"-----BEGIN (?:OPENSSH|RSA|EC|DSA)? ?PRIVATE KEY",public)
 assert not re.search(rb"(?:github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,})",public)
 (out/(value+".log")).write_bytes(public)
 receipt={"job_id":job["id"],"run_id":job["run_id"],"conclusion":job["conclusion"],"raw_bytes":len(b),"raw_sha256":hashlib.sha256(b).hexdigest(),"public_sha256":hashlib.sha256(public).hexdigest(),"synthetic_sha_present":target in text,"synthetic_sha_exact_output_line":bool(re.search(r"^\d{4}-[^\n]+Z "+target+r"\r?$",text,re.M)),"checkout_identity_lines":sha_lines,"redacted_lines":redacted,"no_private_key_marker_in_public":True,"no_github_token_shape_in_public":True}
 (out/(value+"-receipt.json")).write_text(json.dumps(receipt,indent=2)+"\n")
 print(json.dumps({k:receipt[k] for k in ["job_id","conclusion","raw_bytes","synthetic_sha_exact_output_line"]}),flush=True)
