#!/usr/bin/env python3
import argparse, collections, hashlib, json, re
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument("directory",type=Path);p.add_argument("--output",type=Path,required=True);a=p.parse_args()
if a.output.exists():raise SystemExit("refusing overwrite")
raw=a.directory/"stdout.jsonl"; events=[json.loads(line) for line in raw.read_text().splitlines() if line.strip()]
history=[]
pattern=re.compile(r"history verified: phase=(\S+) node=(\d+) rows=(\d+) temporary_retries=(\d+)")
for e in events:
 m=pattern.search(e.get("Output",""))
 if m:history.append(dict(test=e.get("Test"),phase=m[1],node=int(m[2]),rows=int(m[3]),temporary_retries=int(m[4]),time=e.get("Time")))
terminal=[e for e in events if e.get("Action") in ("pass","fail","skip")]
r=dict(raw_sha256=hashlib.sha256(raw.read_bytes()).hexdigest(),result=json.loads((a.directory/"result.json").read_text()),terminal=terminal,action_counts=dict(collections.Counter(e.get("Action") for e in events)),history_count=len(history),history=history,retried_history_count=sum(h["temporary_retries"]>0 for h in history),total_503_temporary_retries=sum(h["temporary_retries"] for h in history),max_503_temporary_retries=max([h["temporary_retries"] for h in history] or [0]))
a.output.write_text(json.dumps(r,indent=2)+"\n")
print(json.dumps({k:v for k,v in r.items() if k not in ("history","terminal")}))
