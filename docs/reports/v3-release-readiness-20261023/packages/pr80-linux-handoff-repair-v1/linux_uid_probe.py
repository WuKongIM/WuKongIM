#!/usr/bin/env python3
"""Small TEST ONLY Linux tmpfs identity probe; no native success claim."""
import json,os,stat,subprocess,sys
from pathlib import Path
assert sys.platform=='linux' and os.geteuid()==0
base=Path('/tmp/uid-probe');base.mkdir(mode=0o777);base.chmod(0o777)
producer=r'''
import json,os,stat
from pathlib import Path
p=Path(__import__('sys').argv[1]);p.mkdir(mode=0o755)
a=p/'resolved-A';a.mkdir(mode=0o700);(a/'bundle-input.json').write_text('{}')
b=p/'A';b.mkdir(mode=0o755);(b/'delivery.json').write_text('{}')
print(json.dumps({'uid':os.geteuid(),'gid':os.getegid(),'paths':[{ 'name':str(q.relative_to(p)), 'uid':q.lstat().st_uid,'gid':q.lstat().st_gid,'mode':oct(stat.S_IMODE(q.lstat().st_mode))} for q in (a,a/'bundle-input.json',b,b/'delivery.json')]}))
'''
consumer=r'''
import json,os,shutil
from pathlib import Path
p=Path(__import__('sys').argv[1]);r={'uid':os.geteuid(),'gid':os.getegid()}
try:r['read']=(p/'resolved-A/bundle-input.json').read_text()
except Exception as e:r['read_error']=type(e).__name__
try:shutil.rmtree(p);r['removed']=not p.exists()
except Exception as e:r['cleanup_error']=type(e).__name__;r['removed']=False
print(json.dumps(r))
'''
def run(code,uid,path):
    def identity():os.setgroups([]);os.setgid(uid);os.setuid(uid)
    p=subprocess.run([sys.executable,'-c',code,str(path)],preexec_fn=identity,capture_output=True,text=True,timeout=10)
    assert p.returncode==0,p.stderr
    return json.loads(p.stdout)
rows=[]
for name,puid,cuid in [('cross_uid',65532,1001),('same_uid',1001,1001)]:
    path=base/name;row={'case':name,'producer':run(producer,puid,path),'consumer':run(consumer,cuid,path)};rows.append(row)
assert rows[0]['consumer']=={'uid':1001,'gid':1001,'read_error':'PermissionError','cleanup_error':'PermissionError','removed':False}
assert rows[1]['consumer']=={'uid':1001,'gid':1001,'read':'{}','removed':True}
print(json.dumps({'test_only':True,'native_fixture_success_claimed':False,'filesystem':'Linux container tmpfs','cases':rows},indent=2))
