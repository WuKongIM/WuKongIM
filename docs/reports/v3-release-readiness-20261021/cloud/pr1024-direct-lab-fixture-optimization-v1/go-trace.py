#!/usr/bin/env python3
import json,os,sys,time,subprocess
p=os.environ['DIRECT_LAB_GO_TRACE']
start=time.monotonic()
with open(p,'a') as f: f.write(json.dumps({'event':'start','argv':sys.argv[1:],'monotonic':start})+'\n')
r=subprocess.run([os.environ['DIRECT_LAB_REAL_GO'],*sys.argv[1:]])
with open(p,'a') as f: f.write(json.dumps({'event':'exit','argv':sys.argv[1:],'elapsed_s':time.monotonic()-start,'exit_code':r.returncode})+'\n')
sys.exit(r.returncode)
