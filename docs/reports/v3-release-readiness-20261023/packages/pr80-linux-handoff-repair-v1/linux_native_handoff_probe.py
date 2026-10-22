#!/usr/bin/env python3
"""TEST ONLY actual native prepare, bound-file read and Linux UID cleanup probe."""
import hashlib,json,os,shutil,stat,subprocess,sys,time
from pathlib import Path
assert sys.platform=='linux' and os.geteuid()==0
base=Path('/tmp/native-uid-probe');base.mkdir(mode=0o777);base.chmod(0o777)
public=base/'public';shutil.copytree('/public-fixture',public)
export_source=json.loads((public/'result.json').read_bytes())['source_sha']
consumer=r'''
import importlib.util,json,os,shutil,sys
from pathlib import Path
spec=importlib.util.spec_from_file_location('native_sdk_drill','/control/tests/provider_sdk/native_object_delivery_sdk_drill.py');d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)
p=Path(sys.argv[1]);sha=sys.argv[2];source=sys.argv[3];r={'uid':os.geteuid(),'gid':os.getegid()}
try:
 value=d.load_bundle(p,sha,source);r['bundle_validated']=True;r['bound_files']=len(value['files'])
except Exception as e:
 r['bundle_error']=type(e).__name__
 try:
  for name in json.loads(p.read_bytes())['files']:
   try:d.facts(Path(name))
   except Exception as failure:r['first_bound_failure']={'path':str(Path(name).relative_to(p.parent)) if Path(name).is_relative_to(p.parent) else 'public-input','error_type':type(failure).__name__};break
 except Exception as other:r['diagnostic_error']=type(other).__name__
try:shutil.rmtree(p.parent);r['removed']=not p.parent.exists()
except Exception as e:r['cleanup_error']=type(e).__name__;r['removed']=False
print(json.dumps(r))
'''
def identity(uid):
 def apply():os.setgroups([]);os.setgid(uid);os.setuid(uid)
 return apply
rows=[]
for name,puid,mode in [('cross_uid',65532,0o777),('same_uid',1001,0o700)]:
 parent=base/name;parent.mkdir(mode=0o777);parent.chmod(0o777)
 # Child owns its scratch; root orchestrator never widens native output modes.
 make="import os;from pathlib import Path;p=Path(__import__('sys').argv[1]);p.mkdir(mode=int(__import__('sys').argv[2],8));p.chmod(int(__import__('sys').argv[2],8))"
 work=parent/'native-sdk-work';subprocess.run([sys.executable,'-c',make,str(work),oct(mode)],preexec_fn=identity(puid),check=True,timeout=10)
 out=parent/'prepare.json';command=[sys.executable,'-B','/control/tests/provider_sdk/native_object_delivery_sdk_drill.py','prepare','--public-dir',str(public),'--work-dir',str(work),'--expected-export-source',export_source,'--source-sha',sys.argv[1],'--output',str(out)]
 start=time.monotonic();p=subprocess.run(command,preexec_fn=identity(puid),capture_output=True,text=True,timeout=900)
 record={'case':name,'producer_uid':puid,'consumer_uid':1001,'prepare_exit':p.returncode,'elapsed_seconds':round(time.monotonic()-start,3),'prepare_stdout':p.stdout[-2048:],'prepare_stderr':p.stderr[-2048:]}
 if p.returncode:
  record['preparation']=json.loads(out.read_bytes()) if out.is_file() else None;rows.append(record);break
 report=json.loads(out.read_bytes());record['preparation_cases']=len(report['cases']);record['bundle_sha256']=report['bundle_sha256']
 record['path_modes']=[{'path':str(path.relative_to(work)),'uid':path.lstat().st_uid,'gid':path.lstat().st_gid,'mode':oct(stat.S_IMODE(path.lstat().st_mode))} for path in [work,work/'prepared/resolved-A',work/'A',work/'A/archives/4242']]
 c=subprocess.run([sys.executable,'-c',consumer,str(work/'bundle.json'),report['bundle_sha256'],sys.argv[1]],preexec_fn=identity(1001),capture_output=True,text=True,timeout=30)
 assert c.returncode==0,c.stderr;record['consumer']=json.loads(c.stdout);rows.append(record)
print(json.dumps({'test_only':True,'filesystem':'Linux tmpfs','source_sha':sys.argv[1],'export_source_sha':export_source,'sdk_20_cases_claimed':False,'cases':rows},indent=2))
assert len(rows)==2 and all(row['prepare_exit']==0 for row in rows)
assert rows[0]['consumer']['bundle_error']=='PermissionError' and rows[0]['consumer']['cleanup_error']=='PermissionError'
assert rows[1]['consumer']['bundle_validated'] and rows[1]['consumer']['removed']
