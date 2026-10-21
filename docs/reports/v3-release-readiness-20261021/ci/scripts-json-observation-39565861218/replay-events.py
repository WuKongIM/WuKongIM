"""Replay the verified small CI artifact; no subprocesses or network."""
import collections, datetime, hashlib, json, pathlib, zipfile
p = pathlib.Path(__file__).resolve().parent
archive = p / "artifact.zip"
verification = json.loads((p / "artifact-verification.json").read_text())
assert archive.stat().st_size == verification["bytes"]
assert "sha256:" + hashlib.sha256(archive.read_bytes()).hexdigest() == verification["sha256"]
with zipfile.ZipFile(archive) as z:
    assert sorted(z.namelist()) == sorted(verification["expected_files"])
    assert z.getinfo("stdout-stderr.log").file_size <= 8 * 1024 * 1024
    lines = z.read("stdout-stderr.log").decode().splitlines()
events = []
non_json = 0
for line in lines:
    try:
        event = json.loads(line)
    except ValueError:
        non_json += 1
        continue
    if isinstance(event, dict) and "Action" in event:
        events.append(event)
pkg = "github.com/WuKongIM/WuKongIM/scripts"
pe = [e for e in events if e.get("Package") == pkg]
start = next(e for e in pe if e["Action"] == "start")
first_cont = next(e for e in pe if e["Action"] == "cont" and "/" not in e.get("Test", ""))
terminal = [e for e in pe if e["Action"] in {"pass", "skip", "fail"} and e.get("Test") and "/" not in e["Test"]]
roots = {e["Test"] for e in pe if e.get("Test") and "/" not in e["Test"]}
def parse(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
result = {"events": len(events), "non_json_lines": non_json, "serial_stage_seconds": (parse(first_cont["Time"]) - parse(start["Time"])).total_seconds(), "observed_package_seconds": (parse(pe[-1]["Time"]) - parse(start["Time"])).total_seconds(), "finished_top_level": len(terminal), "finished_top_level_statuses": dict(collections.Counter(e["Action"] for e in terminal)), "unfinished_top_level": len(roots) - len(terminal), "root_failures": [e["Test"] for e in terminal if e["Action"] == "fail"]}
assert result["finished_top_level_statuses"] == {"pass": 489, "skip": 6, "fail": 10}
assert result["unfinished_top_level"] == 15
print(json.dumps(result, indent=2))
