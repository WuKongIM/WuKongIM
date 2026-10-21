from pathlib import Path
import json,subprocess,time,sys,hashlib
base=Path(__file__).absolute().parent
label=sys.argv[1]
assert label.replace("-", "").isalnum()
output=base/label;output.mkdir();output.chmod(0o777)
prior=json.loads((base/'red-1-command.json').read_text())
command=prior['command']
name='wk-d3-preview-'+label
command[command.index('--name')+1]=name
command=[item.replace(str(base/'red-1'),str(output)) for item in command]
if len(sys.argv)>2 and sys.argv[2] in ('ci','observe','ci-init'):
 command[command.index('--cpus')+1]='1'
 command[command.index('--memory')+1]='512m'
 command[command.index('--pids-limit')+1]='128'
 command[command.index('--tmpfs')+1]='/tmp:rw,nosuid,nodev,size=536870912'
if len(sys.argv)>2 and sys.argv[2]=='ci-init':
 command.insert(2,'--init')
if len(sys.argv)>2 and sys.argv[2]=='observe':
 observation=base/'pid-observations';observation.mkdir();observation.chmod(0o777)
 command[2:2]=['--mount',f'type=bind,src={base / "pid-observer"},dst=/observer,readonly','--mount',f'type=bind,src={observation},dst=/observer-output','--env','PYTHONPATH=/observer']
record={'command':command,'deadline_seconds':900,'started_at_unix':time.time(),'timed_out':False}
w=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-preview-fixture')
record['source_sha256']={str(p.relative_to(w)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [w/'tests/native_stable_candidate_drill.py',w/'tests/native_stable_clients_drill.py',w/'tests/native_object_delivery_fixture_drill.py']}
try:
 r=subprocess.run(command,capture_output=True,timeout=900)
 record.update(exit_code=r.returncode,stdout=r.stdout.decode(),stderr=r.stderr.decode())
except subprocess.TimeoutExpired:
 record.update(timed_out=True,exit_code=None)
finally:
 inspection=subprocess.run(['docker','inspect',name],capture_output=True,timeout=10)
 if inspection.returncode==0:
  value=json.loads(inspection.stdout)[0]
  record['container_state_before_cleanup']=value['State']
 cleanup=subprocess.run(['docker','rm','-f',name],capture_output=True,timeout=30)
 record.update(cleanup_exit_code=cleanup.returncode,finished_at_unix=time.time())
 (base/(label+'-command.json')).write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps({k:record[k] for k in ('exit_code','timed_out','stdout','stderr','cleanup_exit_code')}))
