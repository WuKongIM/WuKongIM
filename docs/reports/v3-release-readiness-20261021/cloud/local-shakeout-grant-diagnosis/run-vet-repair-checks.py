#!/usr/bin/env python3
import pathlib,os,subprocess,json,datetime,hashlib,signal
w=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-shakeout-grant-repair')
r=pathlib.Path(__file__).resolve().parent;out=r/'named-checks-vet-repair';out.mkdir(exist_ok=False)
policyfile=w/'.github/review-agent/policy.json';policy=json.loads(policyfile.read_text())
env=os.environ.copy();env.update(GOCACHE=str(r/'go-cache'),GOWORK='off',GOFLAGS='-p=2')
policy['trusted_checks']['buffer-vet']={'arguments':['go','vet','./pkg/transport/internal/buffer'],'working_dir':'.','timeout_seconds':120}
policy['trusted_checks']['buffer-test']={'arguments':['go','test','./pkg/transport/internal/buffer','-count=1'],'working_dir':'.','timeout_seconds':120}
for name in ['buffer-vet','buffer-test','go-vet','go-format']:
 spec=policy['trusted_checks'][name];cmd=spec['arguments']
 if cmd[0]=='review-agent-check':cmd=['go','run','./cmd/wkreviewcheck']+cmd[1:]
 d={'name':name,'policy_sha256':hashlib.sha256(policyfile.read_bytes()).hexdigest(),'policy_arguments':spec['arguments'] if name not in ['buffer-vet','buffer-test'] else None,'executed_command':cmd,'cwd':str(w/spec['working_dir']),'environment_overrides':{k:env[k] for k in ['GOCACHE','GOWORK','GOFLAGS']},'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
 print(json.dumps({'name':name,'status':'started'}),flush=True)
 with (out/(name+'.log')).open('x') as f:
  p=subprocess.Popen(cmd,cwd=w/spec['working_dir'],env=env,stdout=f,stderr=subprocess.STDOUT,start_new_session=True)
  try:d['exit_code']=p.wait(timeout=spec['timeout_seconds'])
  except subprocess.TimeoutExpired:
   d['timed_out']=True;os.killpg(p.pid,signal.SIGTERM)
   try:p.wait(timeout=10)
   except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL);p.wait()
   d['exit_code']=p.returncode
 d['finished_at']=datetime.datetime.now(datetime.timezone.utc).isoformat();d['log_sha256']=hashlib.sha256((out/(name+'.log')).read_bytes()).hexdigest();(out/(name+'.json')).write_text(json.dumps(d,indent=2)+'\n');print(json.dumps(d),flush=True)
 if d['exit_code']!=0:break
