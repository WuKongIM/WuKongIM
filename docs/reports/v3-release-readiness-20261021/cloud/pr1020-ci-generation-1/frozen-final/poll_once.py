from pathlib import Path
import concurrent.futures,datetime,json,subprocess,sys
root=Path(__file__).resolve().parent
stamp=sys.argv[1]
assert stamp.isdigit()
out=root/("poll-"+stamp)
out.mkdir()
head="0cdfaa37b00c58819895dee54029c27c83c9caa9"
repo="WuKongIM/WuKongIM"
def api(endpoint):
 r=subprocess.run(["gh","api","--method","GET",endpoint],capture_output=True,text=True,timeout=60)
 if r.returncode:raise RuntimeError("read-only GitHub API failed: "+endpoint+" exit "+str(r.returncode))
 return json.loads(r.stdout)
runs=api("repos/"+repo+"/actions/runs?head_sha="+head+"&per_page=100&page=1")
(out/"runs.json").write_text(json.dumps(runs,indent=2)+"\n")
assert len(runs["workflow_runs"])==runs["total_count"] and runs["total_count"]<=100
selected=runs["workflow_runs"]
assert all(r["head_sha"]==head and r["run_attempt"]==1 for r in selected)
def jobs(run):
 j=api("repos/"+repo+"/actions/runs/"+str(run["id"])+"/jobs?per_page=100&page=1")
 assert len(j["jobs"])==j["total_count"] and j["total_count"]<=100
 (out/(str(run["id"])+"-jobs.json")).write_text(json.dumps(j,indent=2)+"\n")
 return {"id":run["id"],"name":run["name"],"status":run["status"],"conclusion":run["conclusion"],"jobs":[{"id":x["id"],"name":x["name"],"status":x["status"],"conclusion":x["conclusion"],"started_at":x["started_at"],"completed_at":x["completed_at"],"active_steps":[s["name"] for s in x.get("steps",[]) if s["status"]=="in_progress"],"failed_steps":[s["name"] for s in x.get("steps",[]) if s["conclusion"]=="failure"]} for x in j["jobs"]]}
summary={"observed_at":datetime.datetime.now(datetime.timezone.utc).isoformat(),"runs":list(concurrent.futures.ThreadPoolExecutor(max_workers=3).map(jobs,selected))}
(out/"summary.json").write_text(json.dumps(summary,indent=2)+"\n")
print(json.dumps(summary,indent=2))
