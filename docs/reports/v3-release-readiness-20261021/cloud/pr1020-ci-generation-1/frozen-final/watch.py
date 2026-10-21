from pathlib import Path
import datetime,json,subprocess,sys,time
root=Path(__file__).resolve().parent
previous=json.loads((root/"poll-03/summary.json").read_text())
for index in range(4,24):
 time.sleep(90)
 result=subprocess.run([sys.executable,"-B",str(root/"poll_once.py"),f"{index:02d}"],capture_output=True,text=True)
 (root/f"poll-{index:02d}-output.json").write_text(result.stdout)
 if result.returncode:
  (root/f"poll-{index:02d}-error.log").write_text(result.stderr)
  print(json.dumps({"event":"read_error","poll":index,"exit":result.returncode}),flush=True);sys.exit(1)
 current=json.loads(result.stdout)
 old={j["id"]:j for r in previous["runs"] for j in r["jobs"]}
 changes=[j for r in current["runs"] for j in r["jobs"] if j!=old.get(j["id"])]
 bad=[j for r in current["runs"] for j in r["jobs"] if j["conclusion"] in ["failure","cancelled","timed_out","action_required"]]
 done=all(r["status"]=="completed" for r in current["runs"])
 print(json.dumps({"event":"poll","poll":index,"observed_at":current["observed_at"],"changed_jobs":changes,"failure_jobs":bad,"all_completed":done,"runs":[{"id":r["id"],"status":r["status"],"conclusion":r["conclusion"]} for r in current["runs"]]}),flush=True)
 previous=current
 if bad or done:sys.exit(0)
print(json.dumps({"event":"bounded_watch_finished","all_completed":False}),flush=True)
