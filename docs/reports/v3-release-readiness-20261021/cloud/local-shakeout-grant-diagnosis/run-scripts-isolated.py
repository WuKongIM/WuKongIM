#!/usr/bin/env python3
import pathlib,os,subprocess,json,datetime,hashlib,signal,time
w=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-shakeout-grant-repair')
r=pathlib.Path(__file__).resolve().parent
out=r/'named-checks-scripts-isolated';out.mkdir(exist_ok=False)
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
policyfile=w/'.github/review-agent/policy.json';policy=json.loads(policyfile.read_text());name='scripts-integration';spec=policy['trusted_checks'][name];cmd=spec['arguments']
env=os.environ.copy();env.update(GOCACHE=str(r/'go-cache'),GOWORK='off',GOFLAGS='-p=2')
selection=json.loads((r/'final-policy-selection.json').read_text());before={p:sha(w/p) for p in selection['paths']}
assert before==selection['file_sha256']
receipt={'name':name,'policy_sha256':sha(policyfile),'policy_arguments':cmd,'executed_command':cmd,'cwd':str(w/spec['working_dir']),'environment_overrides':{k:env[k] for k in ['GOCACHE','GOWORK','GOFLAGS']},'source_file_sha256':before,'reason':'Single parent-authorized isolated repeat after first 540.594s timeout; all other task compilation ended. Original first receipt retained. Same cache path is now warmed, so this is not a pure one-factor contention experiment. No further rerun if timeout.','started_at':now()}
print(json.dumps({'name':name,'status':'started'}),flush=True)
def snapshot(sid):
 rows=[];total_cpu=0.0
 raw=subprocess.check_output(['ps','-axo','pid=,ppid=,pcpu=,rss=,comm='],text=True,timeout=5)
 for line in raw.splitlines():
  f=line.split(None,4)
  if len(f)!=5:continue
  try:
   pid=int(f[0]);cpu=float(f[2]);total_cpu+=cpu
   if os.getsid(pid)==sid:rows.append({'pid':pid,'ppid':int(f[1]),'pcpu':cpu,'rss_kib':int(f[3]),'comm':f[4]})
  except (ValueError,ProcessLookupError,PermissionError):pass
 return {'observed_at':now(),'owned_session':sid,'processes':rows,'all_process_pcpu_sum':total_cpu,'loadavg':os.getloadavg(),'cpu_note':'ps pcpu is platform-averaged percentage; sum may exceed 100 on multi-CPU host, not a complete interval CPU trace.'}
with (out/(name+'.log')).open('x') as log:
 p=subprocess.Popen(cmd,cwd=w/spec['working_dir'],env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True);receipt['pid']=p.pid
 (out/'running.json').write_text(json.dumps(receipt,indent=2)+'\n')
 start=time.monotonic();next_sample=start
 while p.poll() is None:
  tick=time.monotonic()
  if tick>=next_sample:
   try:data=snapshot(p.pid)
   except Exception as e:data={'observed_at':now(),'error_type':type(e).__name__,'error':str(e)}
   with (out/'process-cpu.jsonl').open('a') as f:f.write(json.dumps(data)+'\n')
   next_sample=tick+10
  if tick-start>spec['timeout_seconds']:
   receipt['outer_timeout']=True;os.killpg(p.pid,signal.SIGTERM)
   try:p.wait(timeout=10)
   except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL);p.wait()
   break
  time.sleep(.25)
 receipt['exit_code']=p.wait()
receipt['finished_at']=now();receipt['elapsed_seconds']=time.monotonic()-start;receipt['log_sha256']=sha(out/(name+'.log'));receipt['source_unchanged']=before=={q:sha(w/q) for q in before}
receipt['after_processes']=snapshot(p.pid)
(out/(name+'.json')).write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt),flush=True)
