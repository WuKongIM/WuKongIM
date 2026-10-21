import json,os,signal,subprocess,sys,time
from pathlib import Path
o=Path(sys.argv[1]);mode=sys.argv[2]
(o/'report').mkdir(exist_ok=True)
(o/'report'/'final.json').write_text(json.dumps({'final':True,'verdict':{'outcome':'operator_stop' if mode=='planned130' else 'passed'}}))
(o/'helper.pid').write_text(str(os.getpid()))
if mode in ['cancel','exception','timeout']:
 p=subprocess.Popen([sys.executable,'-c',"import os,signal,time;from pathlib import Path;os.setpgid(0,0);signal.signal(signal.SIGTERM,signal.SIG_IGN);Path("+repr(str(o/'node.pid'))+").write_text(str(os.getpid()));time.sleep(60)"])
 signal.signal(signal.SIGTERM,lambda *_:sys.exit(130))
 (o/'ready').write_text('ready')
 time.sleep(60)
else:
 time.sleep(.1)
 sys.exit(8 if mode=='exit8' else 130 if mode=='planned130' else 0)
