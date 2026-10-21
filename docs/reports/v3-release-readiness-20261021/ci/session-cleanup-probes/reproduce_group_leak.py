import json, os, pathlib, signal, subprocess, sys, time
root=pathlib.Path(__file__).parent
marker=root/'old-child.json'
code='''import json,os,pathlib,subprocess,sys,time
p=subprocess.Popen([sys.executable,'-c','import signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); time.sleep(60)'],process_group=0)
pathlib.Path(sys.argv[1]).write_text(json.dumps({'pid':p.pid,'sid':os.getsid(p.pid),'pgid':os.getpgid(p.pid)}))
time.sleep(60)
'''
driver=subprocess.Popen([sys.executable,'-c',code,str(marker)],start_new_session=True)
child=None
try:
    deadline=time.monotonic()+5
    while not marker.exists():
        assert time.monotonic()<deadline
        time.sleep(.02)
    child=json.loads(marker.read_text())
    assert child['sid']==driver.pid and child['pgid']!=driver.pid
    os.killpg(driver.pid,signal.SIGKILL)
    driver.wait(timeout=2)
    os.kill(child['pid'],0)
    assert os.getsid(child['pid'])==driver.pid
    result={'reproduced':True,'driver_pid':driver.pid,'driver_exit_code':driver.returncode,'surviving_child':child,'old_cleanup_scope':'driver PGID only','expected':'zero live session members'}
    (root/'red-independent-pgid.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result))
finally:
    if child:
        try:
            if os.getsid(child['pid'])==driver.pid:
                os.kill(child['pid'],signal.SIGKILL)
        except ProcessLookupError: pass
    if driver.poll() is None:
        os.killpg(driver.pid,signal.SIGKILL)
        driver.wait(timeout=2)
