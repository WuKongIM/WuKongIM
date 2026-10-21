import datetime, hashlib, json, os, pathlib, subprocess, sys
repo = pathlib.Path(sys.argv[1]).resolve()
root = pathlib.Path(sys.argv[2]).resolve()
root.mkdir(exist_ok=False)
raw = (repo / ".github/review-agent/policy.json").read_bytes()
policy = json.loads(raw)
(root / "policy.json").write_bytes(raw)
names = ["go-format", "flow-doc-contracts", "workflow-contracts", "go-unit", "go-vet", "go-mod-tidy", "scripts-integration"]
env = dict(os.environ, GOWORK="off", GOFLAGS="-p=2", GOCACHE=str(root.parent / "gocache"), LC_ALL="C")
selection = {"source_base": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip(), "policy_sha256": hashlib.sha256(raw).hexdigest(), "checks": {n:policy["trusted_checks"][n] for n in names}, "composite_resolution": "review-agent-check NAME => go run ./cmd/wkreviewcheck NAME", "environment": {k: env[k] for k in ["GOWORK", "GOFLAGS", "GOCACHE", "LC_ALL"]}}
(root / "selection.json").write_text(json.dumps(selection, indent=2)+"\n")
results=[]
for name in names:
    check=policy["trusted_checks"][name]
    args=check["arguments"]
    if args[0] == "review-agent-check":
        args=["go", "run", "./cmd/wkreviewcheck", *args[1:]]
    print(json.dumps({"event":"start", "check":name, "time":datetime.datetime.now(datetime.timezone.utc).isoformat()}), flush=True)
    evidence=root/name
    cmd=[sys.executable, str(repo/"scripts/run-v3-candidate-check.py"), "--evidence-dir",str(evidence),"--timeout-seconds",str(check["timeout_seconds"]),"--max-output-bytes",str(check["max_output_bytes"]),"--",*args]
    result=subprocess.run(cmd, cwd=repo/check["working_dir"],env=env,capture_output=True,text=True)
    (root/(name+"-wrapper.log")).write_text(result.stdout+result.stderr)
    receipt=json.loads((evidence/"result.json").read_text())
    results.append({"check":name,"wrapper_exit_code":result.returncode,**receipt})
    (root/"results.json").write_text(json.dumps(results,indent=2)+"\n")
    print(json.dumps({"event":"finish","check":name,"status":receipt["status"],"exit_code":receipt["exit_code"],"elapsed_seconds":receipt["elapsed_seconds"],"cleanup_complete":receipt["cleanup"]["complete"]}),flush=True)
    if not receipt["cleanup"]["complete"]:
        break
sys.exit(0 if len(results)==len(names) and all(x["passed"] for x in results) else 1)
