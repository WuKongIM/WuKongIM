import pathlib,os,subprocess,json,datetime,hashlib,signal,time
w=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-preclock-diagnostics')
r=pathlib.Path(__file__).resolve().parent;out=r/'named-checks-delta';out.mkdir(exist_ok=False)
policyfile=w/'.github/review-agent/policy.json';policy=json.loads(policyfile.read_text())
env=os.environ.copy();env.update(GOCACHE=str(r/'gocache'),GOWORK='off',GOFLAGS='-p=2',LC_ALL='C')
for name in ['go-format','workflow-contracts']:
 spec=policy['trusted_checks'][name];cmd=spec['arguments']
 if cmd[0]=='review-agent-check':cmd=['go','run','./cmd/wkreviewcheck']+cmd[1:]
 log=out/(name+'.log')
 d={'name':name,'policy_sha256':hashlib.sha256(policyfile.read_bytes()).hexdigest(),'policy_arguments':spec['arguments'],'executed_command':cmd,'cwd':str(w/spec['working_dir']),'environment_overrides':{k:env[k] for k in ['GOCACHE','GOWORK','GOFLAGS','LC_ALL']},'timeout_seconds':spec['timeout_seconds'],'max_output_bytes':spec['max_output_bytes'],'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
 print(json.dumps({'name':name,'status':'started'}),flush=True)
 with log.open('x') as f:
  p=subprocess.Popen(cmd,cwd=w/spec['working_dir'],env=env,stdout=f,stderr=subprocess.STDOUT,start_new_session=True)
  started=time.monotonic()
  try:
   while p.poll() is None:
    if time.monotonic()-started>spec['timeout_seconds'] or log.stat().st_size>spec['max_output_bytes']:
     d['bound_exceeded']=True;break
    time.sleep(.2)
  finally:
   if p.poll() is None:
    os.killpg(p.pid,signal.SIGTERM)
    try:p.wait(timeout=5)
    except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL);p.wait()
  d['exit_code']=p.returncode
 d['finished_at']=datetime.datetime.now(datetime.timezone.utc).isoformat();d['log_sha256']=hashlib.sha256(log.read_bytes()).hexdigest();(out/(name+'.json')).write_text(json.dumps(d,indent=2)+'\n');print(json.dumps(d),flush=True)
 if d['exit_code']!=0:break
