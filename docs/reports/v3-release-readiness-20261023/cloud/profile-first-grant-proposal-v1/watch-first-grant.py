#!/usr/bin/env python3
"""Temporary receipt-fenced, one-node profiling adapter; never starts workload."""
import argparse,datetime,hashlib,json,os,pathlib,signal,subprocess,time
p=argparse.ArgumentParser();p.add_argument('--run-dir',type=pathlib.Path,required=True);p.add_argument('--out-dir',type=pathlib.Path,required=True)
for name in ['source-root','binary','helper']:p.add_argument('--'+name,type=pathlib.Path,required=True)
for name in ['source-revision','binary-sha256','helper-sha256','run-hash','assignment-hash']:p.add_argument('--'+name,required=True)
p.add_argument('--generation',type=int,required=True);p.add_argument('--base-port',type=int,required=True);p.add_argument('--owner-pgid',type=int,required=True);p.add_argument('--deadline-mono',type=float,required=True)
a=p.parse_args();began=time.monotonic();wall=datetime.datetime.now(datetime.timezone.utc);capture=None;stop_reason=None;claimed=False;phase=a.run_dir/'profile-phase';status='skipped';reason='not_started';receipt=None;captured_at=None;escalated=False
utc=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat();sha=lambda b:hashlib.sha256(b).hexdigest()
def handle(sig,frame):
 global stop_reason
 stop_reason='signal_'+signal.Signals(sig).name
for s in [signal.SIGTERM,signal.SIGINT,signal.SIGHUP]:signal.signal(s,handle)
def control_reason():
 if stop_reason:return stop_reason
 if abs((datetime.datetime.now(datetime.timezone.utc)-wall).total_seconds()-(time.monotonic()-began))>1:return 'clock_confounded'
 if (a.run_dir/'evidence/coordinator-exit-status').exists():return 'coordinator_terminal'
 if (a.run_dir/'evidence/profile-stop').exists():return 'supervisor_stop'
 if time.monotonic()>=a.deadline_mono-3:return 'hard_deadline'
 return None
def owned_descendants(root):
 rows=subprocess.check_output(['ps','-axo','pid=,ppid=,pgid='],text=True).splitlines();parents={}
 for line in rows:
  fields=line.split()
  if len(fields)==3 and int(fields[2])==a.owner_pgid:parents[int(fields[0])]=int(fields[1])
 ids=[];front=[root]
 while front:
  current=front.pop();children=[pid for pid,ppid in parents.items() if ppid==current];ids.extend(children);front.extend(children)
 return ids
def cancel():
 global escalated
 if capture is None or capture.poll() is not None:return
 phase.write_text('drain\n');capture.send_signal(signal.SIGTERM)
 try:capture.wait(timeout=2)
 except subprocess.TimeoutExpired:
  escalated=True
  for pid in reversed(owned_descendants(capture.pid)):
   try:os.kill(pid,signal.SIGKILL)
   except ProcessLookupError:pass
  if capture.poll() is None:capture.kill()
  capture.wait(timeout=1)
def finish():
 row={'schema':'wukongim/temporary-first-grant-profile-driver/v1','fixture_or_real_target':'caller_declared_loopback_node2','status':status,'reason':reason,'started_at_utc':wall.isoformat(),'completed_at_utc':utc(),'owner_pgid':a.owner_pgid,'driver_pid':os.getpid(),'capture_pid':None if capture is None else capture.pid,'capture_joined':capture is None or capture.poll() is not None,'kill_escalated':escalated,'helper_exit_status':None if capture is None else capture.returncode,'receipt':receipt,'helper_launch_parent_observed_at':captured_at,'actual_cpu_request_submission_and_real_goroutine_offset':'not_independently_observed;5s is nominal shell timer offset;fake endpoint transcript qualifies only fixture schedule','phase_label_semantics':'measurement file means matching first-grant receipt until observed terminal/stop; natural SEND admission may already be closed','active_send_end_boundary':'unknown;profile may include drain;join run cuts before interpretation','requested_node_id':2,'requested_cpu_seconds':10,'requested_goroutine_offset_seconds':5,'cpu_max_bytes':8388608,'goroutine_max_bytes':4194304,'profile_overhead_affects_measurement':True,'no_performance_acceptance':True}
 dest=a.run_dir/'profile-driver-status.json';temp=dest.with_suffix('.next');temp.write_text(json.dumps(row,indent=2)+'\n');temp.chmod(0o600);temp.replace(dest)
try:
 if a.owner_pgid!=os.getpgrp() or not 0<a.base_port<65533:raise ValueError('owner_or_port_mismatch')
 if a.run_dir.is_symlink() or a.out_dir.is_symlink() or a.out_dir.parent.resolve()!=a.run_dir.resolve():raise ValueError('unsafe_output')
 if time.monotonic()+20>a.deadline_mono:raise ValueError('insufficient_budget')
 revision=subprocess.check_output(['git','-C',str(a.source_root),'rev-parse','HEAD'],text=True,timeout=2).strip()
 if revision!=a.source_revision or sha(a.binary.read_bytes())!=a.binary_sha256 or sha(a.helper.read_bytes())!=a.helper_sha256:raise ValueError('source_mismatch')
 claim=a.run_dir/'profile-driver.claim'
 try:claim.mkdir(mode=0o700);claimed=True
 except FileExistsError:
  print('{"status":"already_claimed","requests":0}');raise SystemExit(0)
 while True:
  why=control_reason()
  if why:raise ValueError(why)
  source=a.run_dir/'report/run-start.json'
  if source.exists() or source.is_symlink():
   if source.is_symlink() or source.stat().st_size>4096:raise ValueError('unsafe_or_oversized_receipt')
   data=source.read_bytes();x=json.loads(data)
   if set(x)!={'schema','stage','started_at','expected_end_at','run_hash','assignment_hash','generation'} or (x['schema'],x['stage'],x['run_hash'],x['assignment_hash'],x['generation'])!=('wukongim.chat_lifecycle.run_start/v1','shakeout',a.run_hash,a.assignment_hash,a.generation):raise ValueError('receipt_identity_mismatch')
   start=datetime.datetime.fromisoformat(x['started_at'].replace('Z','+00:00'));end=datetime.datetime.fromisoformat(x['expected_end_at'].replace('Z','+00:00'));seen=datetime.datetime.now(datetime.timezone.utc)
   if start.tzinfo is None or end.tzinfo is None or (end-start).total_seconds()!=259200:raise ValueError('receipt_time_invalid')
   lag=(seen-start).total_seconds()
   if not 0<lag<=2:raise ValueError('missed_early_window')
   if control_reason():raise ValueError(control_reason())
   if time.monotonic()+20>a.deadline_mono:raise ValueError('insufficient_budget_after_receipt_wait')
   receipt={'sha256':sha(data),'run_hash':x['run_hash'],'assignment_hash':x['assignment_hash'],'generation':x['generation'],'started_at':x['started_at'],'seen_at':seen.isoformat(),'detection_lag_seconds':lag,'publication_lag_limit':'receipt is written after first grant and dataset probe; exact sampling at first grant is impossible'}
   phase.write_text('measurement\n');phase.chmod(0o600);captured_at=utc()
   with (a.run_dir/'profile-helper.log').open('wb') as log:
    capture=subprocess.Popen(['bash',str(a.helper),'--out-dir',str(a.out_dir),'--phase-state-file',str(phase),'--trigger-kind','first_grant_receipt','--trigger-observed-phase','measurement','--previous-utc',start.isoformat(),'--current-utc',seen.isoformat(),'--node','http://127.0.0.1:'+str(a.base_port+2),'--cpu-seconds','10'],stdout=log,stderr=subprocess.STDOUT)
   capture_start=time.monotonic()
   while capture.poll() is None:
    why=control_reason()
    if why or time.monotonic()-capture_start>=15:
     reason=why or 'capture_deadline';status='partial';cancel();break
    time.sleep(.02)
   capture.wait(timeout=1)
   if status!='partial':
    m=json.loads((a.out_dir/'metadata.json').read_text());status='complete' if m['capture']['valid'] else 'partial';reason=m['capture']['reason']
   break
  time.sleep(.05)
except SystemExit:raise
except Exception as error:
 status='partial' if capture else 'skipped';reason=str(error) if isinstance(error,ValueError) else type(error).__name__;cancel()
finally:
 if claimed or reason!='not_started':
  cancel();finish()
