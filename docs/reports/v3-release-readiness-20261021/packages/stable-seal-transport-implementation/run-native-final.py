import hashlib,json,pathlib,subprocess,time,datetime
p=pathlib.Path(__file__).parent
root=pathlib.Path("/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-stable-seal-transport")
command=json.loads((p/"native-first-result.json").read_text())["command"]
command=[x.replace("wk-stable-seal-transport-first","wk-stable-seal-transport-final-20261021c").replace("/native-first:/evidence","/native-final:/evidence") for x in command]
(p/"native-final").mkdir(exist_ok=False)
identity={"baseline":"c8aae37d13787a0aceceac751d6d35bbc1d89c35","source_files":{str(f.relative_to(root)):hashlib.sha256(f.read_bytes()).hexdigest() for kind in ("scripts","tests") for f in sorted((root/kind).glob("*.py"))}}
(p/"native-final-inputs.json").write_text(json.dumps(identity,indent=2)+"\n")
start=time.monotonic();outcome={"command":command,"started_utc":datetime.datetime.now(datetime.timezone.utc).isoformat(),"source_identity":"uncommitted working bytes frozen in native-final-inputs.json; source_sha is baseline only"}
with (p/"native-final.log").open("wb") as log:
 try:
  result=subprocess.run(command,stdout=log,stderr=subprocess.STDOUT,timeout=600,check=False)
  outcome["exit_code"]=result.returncode
 except subprocess.TimeoutExpired:
  outcome["timeout_seconds"]=600
  subprocess.run(["/usr/local/bin/docker","--host","unix:///Users/tt/.docker/run/docker.sock","kill","wk-stable-seal-transport-final-20261021c"],capture_output=True,timeout=15)
outcome["elapsed_seconds"]=time.monotonic()-start
(p/"native-final-result.json").write_text(json.dumps(outcome,indent=2)+"\n")
print(json.dumps(outcome))
