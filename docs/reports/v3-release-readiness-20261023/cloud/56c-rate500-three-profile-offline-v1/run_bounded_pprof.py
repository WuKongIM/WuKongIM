import datetime,hashlib,json,os,pathlib,selectors,signal,subprocess,time
import offline_adapter as A
BASE=pathlib.Path('/tmp/wukongim-v3-readiness-20261023');OUT=BASE/'cloud/56c-rate500-three-profile-offline-v1';PRIVATE=BASE/'private/56c-rate500-three-profile-offline-v1';PLAN=json.loads((BASE/'cloud/56c-three-profile-offline-proposal-v1/offline-command-plan.json').read_text());APPROVAL=json.loads((BASE/'cloud/56c-three-profile-offline-root-approval-v1/approval.json').read_text());ROWS={f"node-{r['node_id']}-{r['kind']}":r for r in APPROVAL['inputs']};allowed=A.fixed_paths(BASE/'cloud/full-original-distribution-56c-rate500-three-profile-v1');receipts=[]
TOOL=pathlib.Path(APPROVAL['tool']['path']);assert hashlib.sha256(TOOL.read_bytes()).hexdigest()==APPROVAL['tool']['verified_sha256']
# Exclusive claim guards this approved fixed command set against accidental repeat.
(OUT/'pprof-once.claim').mkdir(mode=0o700)
for cmd in PLAN['commands']:
 row=ROWS[cmd['profile']];receipt={'argv':cmd['argv'],'env':PLAN['environment'],'started_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'timeout_seconds':30,'maximum_stdout_bytes':1048576,'maximum_stderr_bytes':16384,'tool_sha256':APPROVAL['tool']['verified_sha256'],'input_before':A.verify_input(row,allowed)};started=time.monotonic();deadline=started+30;process=None;reason=None;counts={'stdout':0,'stderr':0};paths={k:pathlib.Path(cmd[k+'_private_path']) for k in ['stdout','stderr']};outputs={k:p.open('xb') for k,p in paths.items()}
 try:
  process=subprocess.Popen(cmd['argv'],env=PLAN['environment'],cwd=PRIVATE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
  selector=selectors.DefaultSelector()
  for name,stream in [('stdout',process.stdout),('stderr',process.stderr)]:os.set_blocking(stream.fileno(),False);selector.register(stream,selectors.EVENT_READ,name)
  while selector.get_map():
   if time.monotonic()>=deadline:reason='timeout';break
   for key,event in selector.select(min(.1,max(0,deadline-time.monotonic()))):
    data=os.read(key.fd,65536)
    if not data:selector.unregister(key.fileobj);continue
    name=key.data;cap=1048576 if name=='stdout' else 16384;keep=data[:max(0,cap-counts[name])];outputs[name].write(keep);counts[name]+=len(keep)
    if len(keep)!=len(data):reason=name+'_oversized';break
   if reason:break
  if reason:
   try:os.killpg(process.pid,signal.SIGKILL)
   except ProcessLookupError:pass
  process.wait(timeout=max(.001,deadline-time.monotonic()) if not reason else 1)
  receipt['exit_status']=process.returncode;receipt['status']='complete' if not reason and process.returncode==0 else 'partial';receipt['error_class']=reason or (None if process.returncode==0 else 'pprof_nonzero')
 except Exception as error:receipt['status']='partial';receipt['error_class']=A.closed_error(error)
 finally:
  if process is not None and process.poll() is None:
   try:os.killpg(process.pid,signal.SIGKILL)
   except ProcessLookupError:pass
   process.wait(timeout=1)
  for f in outputs.values():f.close()
  for p in paths.values():p.chmod(0o600)
 receipt['elapsed_seconds']=time.monotonic()-started;receipt['completed_at_utc']=datetime.datetime.now(datetime.timezone.utc).isoformat();receipt['stdout_private_bytes']=counts['stdout'];receipt['stderr_private_bytes']=counts['stderr'];receipt['private_output_sha256']={k:hashlib.sha256(p.read_bytes()).hexdigest() for k,p in paths.items()};receipt['input_after']=A.verify_input(row,allowed);receipts.append(receipt);(OUT/'pprof-commands.json').write_text(json.dumps(receipts,indent=2)+'\n');print(json.dumps({'profile':cmd['profile'],'mode':cmd['mode'],'status':receipt['status'],'error_class':receipt['error_class'],'elapsed_seconds':receipt['elapsed_seconds'],'private_stdout_bytes':counts['stdout'],'private_stderr_bytes':counts['stderr']}),flush=True)
assert len(receipts)==12
