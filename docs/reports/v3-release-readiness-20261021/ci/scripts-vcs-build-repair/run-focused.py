import datetime, hashlib, json, os, pathlib, signal, subprocess, time
p = pathlib.Path(__file__).resolve().parent
root = pathlib.Path("/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-shakeout-grant-repair")
command = ["go", "test", "-tags=integration", "./scripts/...", "-run=^TestWukongIMThreeNodeLocalE2EBuildUsesSelectedWorktreeRevision$", "-count=1", "-timeout=9m", "-parallel=2"]
env = dict(os.environ, GOWORK="off", GOFLAGS="-p=2 -json")
(p / "focused-command.json").write_text(json.dumps({"argv": command, "cwd": str(root), "environment_overrides": {"GOWORK": "off", "GOFLAGS": "-p=2 -json"}, "outer_timeout_seconds": 600, "performance_comparison": False, "known_possible_concurrency": "rc temporary GNU Bash build -j2"}, indent=2) + "\n")
started = datetime.datetime.now(datetime.timezone.utc).isoformat()
t0 = time.monotonic()
with (p / "focused.stdout-stderr.log").open("wb") as output:
    process = subprocess.Popen(command, cwd=root, env=env, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
    timed_out = False
    try:
        rc = process.wait(timeout=600)
    except subprocess.TimeoutExpired:
        timed_out = True
        os.killpg(process.pid, signal.SIGTERM)
        try:
            rc = process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            rc = process.wait(timeout=5)
log = (p / "focused.stdout-stderr.log").read_bytes()
result = {"started_utc": started, "finished_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(), "elapsed_seconds": time.monotonic() - t0, "exit_code": rc, "timed_out": timed_out, "output_bytes": len(log), "output_sha256": hashlib.sha256(log).hexdigest()}
(p / "focused-result.json").write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps(result))
raise SystemExit(124 if timed_out else rc)
