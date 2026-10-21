#!/usr/bin/env python3
"""Offline collector probe. All transport calls go to a fixture, never SSH."""
import json, os, pathlib, subprocess, sys, tempfile
root = pathlib.Path(sys.argv[1]).resolve()
out = pathlib.Path(__file__).resolve().parent
fixture = r'''#!/usr/bin/env python3
import json, os, sys
command = sys.argv[-1]
if "worker-evidence.json" in command:
    kind = "worker-evidence"
    body = b'{"schema":"wukongim/chat-lifecycle-worker-evidence/v1","workers":[]}\n'
elif "diagnostic-status.json" in command:
    kind = "diagnostic-status"
    body = b'{"schema":"wukongim/chat-lifecycle-diagnostic-status/v1"}\n'
elif "api/v1/query_range" in command:
    kind = "metrics-range"
    if os.environ["PROBE_MODE"] == "oversized":
        body = json.dumps({"status":"success","data":{"result":[],"fixture_padding":"x"*(4194304+100)}}).encode()
    else:
        body = b'{"status":"success","data":{"result":[]}}\n'
elif "api/v1/targets" in command:
    kind = "metrics-targets"
    body = b'{"status":"success","data":{"activeTargets":[]}}\n'
else:
    kind = "other-terminal-evidence"
    body = b'fixture terminal evidence\n'
with open(os.environ["PROBE_CALLS"], "a") as f:
    f.write(kind+"\n")
# Emulate the existing remote head bound without executing the remote command.
assert command.endswith(" | head -c 4194305"), command
sys.stdout.buffer.write(body[:4194305])
'''
results=[]
with tempfile.TemporaryDirectory(prefix="wk-collector-offline-") as temp:
    temp=pathlib.Path(temp); bin=temp/"bin";bin.mkdir()
    for name, body in {"ssh":fixture, "timeout":"#!/bin/sh\nshift\nexec \"$@\"\n".replace('\\"','"')}.items():
        p=bin/name;p.write_text(body);p.chmod(0o700)
    # No actual timer is required: fake SSH executes only deterministic fixture code.
    (bin/"timeout").write_text('#!/bin/sh\nshift\nexec "$@"\n')
    for mode in ["normal", "oversized"]:
        target=temp/mode;calls=temp/(mode+"-calls.txt")
        env=dict(os.environ,PATH=str(bin)+os.pathsep+os.environ["PATH"],PROBE_MODE=mode,PROBE_CALLS=str(calls),WK_CLOUD_SSH_CONFIG=str(temp/"fake-config"),WK_CLOUD_SERVICE1_IP="fixture-service-1",WK_CLOUD_SERVICE2_IP="fixture-service-2",WK_CLOUD_SERVICE3_IP="fixture-service-3")
        run=subprocess.run(["bash",str(root/"scripts/chat-lifecycle/collect-terminal-evidence.sh"),"rehearsal",str(target)],env=env,cwd=root,capture_output=True,timeout=30)
        manifest=json.loads((target/"manifest.json").read_text())
        row={"mode":mode,"exit_code":run.returncode,"manifest":manifest,"transport_call_kinds":calls.read_text().splitlines(),"worker_evidence_requested":"worker-evidence" in calls.read_text().splitlines(),"worker_evidence_retained":any(p.name=="worker-evidence.json" for p in target.rglob("*")),"range_retained":(target/"metrics/prometheus-range.json").exists(),"range_temporary_retained":(target/"metrics/prometheus-range.json.tmp").exists(),"stdout_bytes":len(run.stdout),"stderr_bytes":len(run.stderr)}
        results.append(row)
normal,oversized=results
assert normal["exit_code"]==0 and normal["manifest"]["complete"] and len(normal["manifest"]["captures"])==16
assert not normal["worker_evidence_requested"] and not normal["worker_evidence_retained"]
assert oversized["exit_code"]==1 and not oversized["manifest"]["complete"]
assert oversized["manifest"]["capture_failures"]==1
assert oversized["manifest"]["captures"][-1]=={"class":"metrics","name":"range","state":"oversized","bytes":4194305}
assert not oversized["range_retained"] and not oversized["range_temporary_retained"]
(out/"probe-results.json").write_text(json.dumps({"schema":"wukongim/offline-collector-probe/v1","fixture_only":True,"actual_remote_calls":0,"cases":results,"desired_worker_retention_assertion":"FAIL: existing collector never requests the available worker-evidence fixture","production_message_loss_reproduced":False},indent=2)+"\n")
print("normal: complete=true, 16 captures, worker-evidence never requested (retention assertion RED)")
print("oversized: exit=1, complete=false, range=oversized/4194305 bytes, no retained range or temporary file")

if "--expect-worker-retained" in sys.argv:
    if not normal["worker_evidence_retained"]:
        print("FAIL: worker-evidence fixture was available but collector did not retain it", file=sys.stderr)
        sys.exit(1)
