import json,os,pathlib,signal,subprocess,sys,time
args=dict(zip(sys.argv[1::2],sys.argv[2::2]));node=int(args['--node-id']);root=pathlib.Path(os.environ['FAKE_ROOT']);mode=os.environ['ORPHAN_MODE'];(root/f'node-{node}.pid').write_text(str(os.getpid()))
child_program=pathlib.Path(__file__).with_name('fake-ignoreterm-child.py')
child=subprocess.Popen([sys.executable,str(child_program),str(root),str(node)],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
(root/f'child-{node}.pid').write_text(str(child.pid));end=time.monotonic()+1
while not (root/f'child-{node}.ready').exists() and time.monotonic()<end:time.sleep(.005)
if mode=='exit-before-cancel':raise SystemExit(3)
if mode=='ignore-term':signal.signal(signal.SIGTERM,signal.SIG_IGN)
while True:time.sleep(1)
