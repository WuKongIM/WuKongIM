from pathlib import Path
import subprocess,json,time,hashlib
base=Path(__file__).parent
name='wk-m2a-native-route-readback-first'
command=['docker','run','--init','--name',name,'--platform','linux/amd64','--user','65532:65532','--read-only','--network','none','--cap-drop','ALL','--security-opt','no-new-privileges','--cpus','1','--memory','512m','--pids-limit','128','--tmpfs','/tmp:rw,nosuid,nodev,size=536870912','--mount','type=bind,src=/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-preview-fixture,dst=/source,readonly','--mount','type=bind,src=/tmp/wukongim-v3-readiness-20261021/packages/provider-preview-fixture-implementation/ci-init-native-1,dst=/public,readonly','--mount','type=bind,src='+str(base/'readback.py')+',dst=/readback.py,readonly','--mount','type=bind,src='+str(base/'first-run')+',dst=/output','--env','PYTHONDONTWRITEBYTECODE=1','ghcr.io/wukongim/native-package-signing-toolchain@sha256:769d49a4882439a35fdf4497a4b97f09843a9dc92f4074447d48e43b2614d922','python3','-B','/readback.py']
record={'command':command,'deadline_seconds':900,'started_at_unix':time.time(),'timed_out':False,'readback_script_sha256':hashlib.sha256((base/'readback.py').read_bytes()).hexdigest()}
try:
 result=subprocess.run(command,capture_output=True,timeout=900);record.update(exit_code=result.returncode,stdout=result.stdout.decode(),stderr=result.stderr.decode())
except subprocess.TimeoutExpired as e:
 record.update(exit_code=None,timed_out=True,stdout=(e.stdout or b'').decode(),stderr=(e.stderr or b'').decode())
finally:
 status=subprocess.run(['docker','inspect',name],capture_output=True,timeout=10)
 if status.returncode==0:record['container_state']=json.loads(status.stdout)[0]['State']
 cleanup=subprocess.run(['docker','rm','-f',name],capture_output=True,timeout=30)
 record['cleanup_exit_code']=cleanup.returncode;record['finished_at_unix']=time.time()
 (base/'first-command.json').write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps({k:record[k] for k in ('exit_code','timed_out','stdout','stderr','cleanup_exit_code')}))
