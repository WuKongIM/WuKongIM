"""Manual integration probe: real processes; deliberately kept outside default unit tier."""
import importlib.util, json, os, pathlib, signal, subprocess, sys, time
root=pathlib.Path(__file__).parent
helper=pathlib.Path(sys.argv[1])
spec=importlib.util.spec_from_file_location('candidate_runner',helper)
mod=importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
child_code='import signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); time.sleep(60)'
driver_code='''import json,os,pathlib,subprocess,sys,time
p=subprocess.Popen([sys.executable,'-c',sys.argv[2]],process_group=0)
pathlib.Path(sys.argv[1]).write_text(json.dumps({'pid':p.pid,'sid':os.getsid(p.pid),'pgid':os.getpgid(p.pid)}))
time.sleep(.1)
mode=sys.argv[3]
if mode=='exit7': sys.exit(7)
if mode=='pass': sys.exit(0)
if mode=='output':
    while True: print('x'*8192,flush=True)
time.sleep(60)
'''
results={}
sentinel=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)'],start_new_session=True)
try:
    for mode,expected in [('pass','passed'),('exit7','failed'),('timeout','timeout'),('output','output_limit')]:
        out=root/mode; out.mkdir(exist_ok=True)
        command=[sys.executable,'-c',driver_code,str(out/'child.json'),child_code,mode]
        result=mod.run_command(command,pathlib.Path.cwd(),out,.6 if mode=='timeout' else 5,4096)
        assert result['status']==expected,result
        assert result['cleanup']['verified'] and result['cleanup']['complete'],result
        assert result['cleanup']['remaining_processes']==[],result
        assert not mod.session_members(result['session_id']),result
        assert sentinel.poll() is None,'cleanup killed unrelated session'
        if mode=='exit7': assert result['exit_code']==7 and result['wrapper_exit_code']==7,result
        if mode=='output': assert result['output_truncated'],result
        results[mode]=result
    out=root/'cancel'; out.mkdir(exist_ok=True)
    command=[sys.executable,'-c',driver_code,str(out/'child.json'),child_code,'sleep']
    wrapper=subprocess.Popen([sys.executable,str(helper),'--evidence-dir',str(out),'--timeout-seconds','30','--max-output-bytes','4096','--',*command],start_new_session=True)
    deadline=time.monotonic()+5
    while not (out/'child.json').exists():
        assert time.monotonic()<deadline
        time.sleep(.02)
    wrapper.send_signal(signal.SIGTERM)
    assert wrapper.wait(timeout=8)==143
    result=json.loads((out/'result.json').read_text())
    assert result['status']=='interrupted' and result['cleanup']['complete'],result
    assert not mod.session_members(result['session_id'])
    assert sentinel.poll() is None
    results['cancel']=result
    out=root/'enumeration_failure'; out.mkdir(exist_ok=True)
    command=[sys.executable,'-c',driver_code,str(out/'child.json'),child_code,'pass']
    original=mod.session_members
    def fail(_sid): raise RuntimeError('probe denied process enumeration')
    mod.session_members=fail
    try:
        result=mod.run_command(command,pathlib.Path.cwd(),out,5,4096)
        assert result['status']=='cleanup_failed' and result['command_status']=='passed',result
        assert result['wrapper_exit_code']!=0 and not result['cleanup']['verified'],result
        results['enumeration_failure']=result
    finally:
        mod.session_members=original
        if (out/'child.json').exists():
            assert mod.cleanup_session(json.loads((out/'child.json').read_text())['sid'])['complete']
    own=mod.cleanup_session(os.getsid(0))
    assert not own['verified'] and not own['complete'],own
    results['refuses_caller_session']=own
finally:
    if sentinel.poll() is None:
        os.killpg(sentinel.pid,signal.SIGKILL)
        sentinel.wait(timeout=2)
(root/'green-owned-session-cases.json').write_text(json.dumps(results,indent=2)+'\n')
print(json.dumps({key:value.get('status',value.get('error')) for key,value in results.items()}))
