from pathlib import Path
import json,re,collections,hashlib,datetime
p=Path(__file__).resolve().parent
head="b3fdb777b9270128fe56086bc47562b321c2c947";merge="8805fe2e09d4171ff761bfa4699859c6ba4fed17";base="aeb45c2164b6a50214928f520edd2a8815b7a976";tree="ae5a4175337f10f521a13a6c704c1b3040cf2bd2"
def read(f):return json.loads(f.read_text())
assert read(p/"head-git-commit.json")["tree"]["sha"]==tree==read(p/"merge-git-commit.json")["tree"]["sha"]
runs={};jobs={};arts={};page_coverage=[]
for snapshot in sorted((p/"snapshots").iterdir()):
 for f in snapshot.glob("*-run.json"):
  r=read(f);assert r["run_attempt"]==1 and r["head_sha"]==head;runs[r["id"]]=r
 for part,target in [("jobs",jobs),("artifacts",arts)]:
  for f in snapshot.glob("*-"+part+".json"):
   r=read(f);assert r["total_count"]==len(r[part]) and len(r[part])<100
   page_coverage.append({"path":str(f.relative_to(p)),"total_count":r["total_count"],"returned":len(r[part]),"complete":True})
   for v in r[part]:target[v["id"]]=v
summary={"observed_at":datetime.datetime.now(datetime.timezone.utc).isoformat(),"head":head,"base":base,"checkout":merge,"tree":tree,"runs":[{k:r.get(k) for k in ["id","name","event","status","conclusion","head_sha","run_attempt","created_at","updated_at","html_url"]} for r in runs.values()],"candidate":{},"performance":{},"page_coverage":page_coverage,"all_runs_terminal":all(r["status"]=="completed" for r in runs.values()),"all_runs_success":all(r["conclusion"]=="success" for r in runs.values())}
summary["jobs"]=[{k:j.get(k) for k in ["id","run_id","name","head_sha","run_attempt","status","conclusion","started_at","completed_at","steps"]} for j in jobs.values()]
for a in arts.values():
 d=p/"artifacts-public"/str(a["id"])/"members"
 if not d.exists():continue
 if a["name"].startswith("v3-candidate-"):
  label=a["name"].split("-")[2];ident=read(d/"identity.json");assert ident["source_sha"]==merge and ident["run_attempt"]=="1" and ident["policy_sha"]==base
  results={}
  for f in d.rglob("result.json"):
   r=read(f)
   if r["status"]!="not_run":assert not r["output_truncated"] and r["cleanup"]["verified"] and r["cleanup"]["complete"] and not r["cleanup"]["remaining_processes"]
   results[str(f.relative_to(d))]=r
  row={"artifact_id":a["id"],"identity":ident,"results":results}
  if label in ("default","scripts"):
   policy=read(d/"policy.json");command=read(d/"command.json");definition=policy["trusted_checks"]["go-unit" if label=="default" else "scripts-integration"]
   assert all(command[k]==definition[k] for k in ["arguments","working_dir","timeout_seconds","max_output_bytes"])
   row["command"]=command;row["policy_sha256"]=hashlib.sha256((d/"policy.json").read_bytes()).hexdigest();assert row["policy_sha256"]==ident["policy_sha256"]
  if label=="default":
   s=(d/"stdout-stderr.log").read_text();row["packages_passed"]=len(re.findall(r"^ok\s",s,re.M));row["packages_without_tests"]=len(re.findall(r"^\?\s",s,re.M));row["replication_package_pass_lines"]=[x for x in s.splitlines() if x.startswith("ok") and "/pkg/channel/replication" in x];row["new19_case_evidence_limit"]="Complete exact-source package executed without -run; Linux output is package-level, not per-subtest. Local 19-case event receipt is separately preserved."
  elif label=="scripts":
   ev=read(d.parent/"test-events.json");row["root_terminal_counts"]=dict(collections.Counter(e["Action"] for e in ev if "/" not in e["Test"] and e["Action"] in ("pass","fail","skip")));row["failed_events"]=[e for e in ev if e["Action"]=="fail"];row["budget_events"]=[e for e in ev if "DownwardBudget" in e["Test"]];row["unfinished_tests"]=sorted({e["Test"] for e in ev if e["Action"]=="run"}-{e["Test"] for e in ev if e["Action"] in ("pass","fail","skip")})
  elif label=="e2e":
   row["test_events"]=read(d.parent/"test-events.json");row["history_checks"]=[]
   f=d/"ordinary_history/stdout.jsonl"
   if f.exists():
    for line in f.read_text().splitlines():
     try:e=json.loads(line)
     except json.JSONDecodeError:continue
     m=re.search(r"history verified: phase=(\S+) node=(\d+) rows=(\d+) temporary_retries=(\d+)",e.get("Output",""))
     if m:row["history_checks"].append({"test":e.get("Test"),"phase":m[1],"node":int(m[2]),"rows":int(m[3]),"temporary_retries":int(m[4])})
   row["history_count"]=len(row["history_checks"]);row["history_503_retries"]=sum(h["temporary_retries"] for h in row["history_checks"])
  summary["candidate"][label]=row
 elif a["name"].startswith("500qps-"):
  arrival=read(d/"arrival.json");result=read(d/"result.json");assert len(arrival["windows"])==3
  name=a["name"].removeprefix("500qps-").split("-"+merge)[0]
  summary["performance"][name]={"artifact_id":a["id"],"result":result,"phase":arrival["phase"],"qualification":arrival["qualification"],"windows":arrival["windows"],"maximum_second_p99_ms":max(s["scheduled_to_completion_p99_ms"] for s in arrival["seconds"])}
summary["limitations"]=["Read-only first attempt; no CI reruns, dispatch, source changes or cloud actions.","Native preview is not signed public stable channel publication.","Large correctness/native ZIPs use selected authenticated ranges; no claimed independent whole-ZIP SHA.","A passing workflow does not alone prove every numeric assertion; artifact receipts are checked separately."]
(p/"observed-analysis.json").write_text(json.dumps(summary,indent=2)+"\n")
print(json.dumps({"all_terminal":summary["all_runs_terminal"],"all_success":summary["all_runs_success"],"candidate_available":list(summary["candidate"]),"performance_available":list(summary["performance"])}))
