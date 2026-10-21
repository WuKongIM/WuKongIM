from pathlib import Path
import subprocess,json,time,datetime,hashlib,sys
WORK=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-stable-native-clients')
OUT=Path(__file__).resolve().parent
FILES=['CHANGELOG.md','docs/stable-audit-transport.md','tests/native_stable_clients_drill.py','tests/test_native_stable_clients_drill.py']
def identities():
    return {name: hashlib.sha256((WORK/name).read_bytes()).hexdigest() for name in FILES}
commands=[('03-related',['python3','-m','unittest','discover','-s','tests','-p','test_native_stable_clients_drill.py','-v'],120),
          ('04-full-python',['python3','-m','unittest','discover','-s','tests','-p','test_*.py'],300),
          ('05-contract',['./tests/contract.sh'],120),
          ('06-diff-check',['git','diff','--check'],30)]
for name,command,timeout in commands:
    initial=identities(); start=time.monotonic()
    with (OUT/(name+'.log')).open('wb') as log:
        result=subprocess.run(command,cwd=WORK,stdout=log,stderr=subprocess.STDOUT,timeout=timeout)
    record={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':command,'cwd':str(WORK),'exit_code':result.returncode,'elapsed_seconds':round(time.monotonic()-start,3),'before':initial,'after':identities(),'log_sha256':hashlib.sha256((OUT/(name+'.log')).read_bytes()).hexdigest()}
    assert record['before']==record['after']
    (OUT/(name+'.json')).write_text(json.dumps(record,indent=2)+'\n')
    print(json.dumps({'check':name,'exit_code':result.returncode,'elapsed_seconds':record['elapsed_seconds']}),flush=True)
    if result.returncode: sys.exit(result.returncode)
