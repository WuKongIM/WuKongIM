from pathlib import Path
import datetime,hashlib,json,os,stat,subprocess
D=Path(__file__).parent;O=D/'bounded-unit-read';O.mkdir(mode=0o700);C='e30299fa59ea6669ea71024eabe209840f241f19'
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
identity=json.loads((D.parent/'rc-start-fc182/local-identity.json').read_text());r=json.loads((D/'deployment/verified/deployment-outcome.json').read_text())['receipt'];a=json.loads((D/'acquire/verified/receipt.json').read_text())['receipt'];assert r['lease_id']==a['lease_id']=='chat-20261021T095421Z-cf11d65c-rehearsal-2';h=next(h for h in r['hosts'] if h['role']=='load');assert any(x['id']==h['instance_id'] and x['role']=='load' and x['kind']=='instance' for x in a['resources']);key=Path(identity['private_key_path']);assert stat.S_IMODE(key.stat().st_mode)==0o600;assert identity['public_key']==json.loads((D/'planned-workflow-inputs.json').read_text())['codex_diagnostic_pubkey']
for name in ['classify-stage-service-state.sh','pre-clock-diagnostic.py']:
 raw=subprocess.check_output(['git','show',C+':scripts/chat-lifecycle/'+name]);(O/name).write_bytes(raw)
trust=subprocess.check_output(['git','show',C+':scripts/cloud-deployment/write-ssh-config.sh']);(O/'ssh-trust-basis.json').write_text(json.dumps({'source_sha':C,'source_path':'scripts/cloud-deployment/write-ssh-config.sh','source_sha256':hashlib.sha256(trust).hexdigest(),'policy':'same BatchMode/IdentitiesOnly/accept-new/exact per-task known_hosts as existing contract','private_key_bytes_read_printed_or_copied':False,'private_key_used_only_by_ssh':True,'target_from_verified_deployment_receipt':h['instance_id']},indent=2)+'\n')
kh=O/'known_hosts';kh.touch(mode=0o600)
remote="sudo systemctl show 'wkbench-rehearsal.service' --property=ActiveState --property=Job --property=LoadState --property=SubState --property=Result --property=ExecMainCode --property=ExecMainStatus --no-pager"
cmd=['ssh','-F','/dev/null','-i',str(key),'-o','IdentitiesOnly=yes','-o','BatchMode=yes','-o','ConnectTimeout=10','-o','ConnectionAttempts=1','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=3','-o','StrictHostKeyChecking=accept-new','-o','UserKnownHostsFile='+str(kh),'wkdeploy@'+h['public_address'],remote]
record={'schema':'wukongim.bounded_unit_read/v1','started_at':now(),'remote_command':remote,'target_instance_id':h['instance_id'],'lease_id':r['lease_id'],'note':'Post-failure state, not original before-kill state; one read only. No environment/journal/secrets requested.'}
try:
 p=subprocess.run(cmd,capture_output=True,timeout=65);record.update(ended_at=now(),ssh_exit=p.returncode,stdout_bytes=len(p.stdout),stdout_sha256=hashlib.sha256(p.stdout).hexdigest(),stderr_bytes=len(p.stderr),stderr_sha256=hashlib.sha256(p.stderr).hexdigest())
 if p.returncode==0:
  assert len(p.stdout)<=8192
  reducer=subprocess.run(['python3',str(O/'pre-clock-diagnostic.py'),'state'],input=p.stdout,capture_output=True,check=True);unit=json.loads(reducer.stdout);(O/'unit.json').write_bytes(reducer.stdout);record['unit']=unit
  raw=p.stdout.rstrip(b'\n');f=subprocess.run(['sed','-E','/^(LoadState|SubState|Result|ExecMainCode|ExecMainStatus)=/d'],input=raw+b'\n',capture_output=True,check=True);q=subprocess.run(['/bin/bash',str(O/'classify-stage-service-state.sh')],input=f.stdout,capture_output=True);record.update(classifier_exit=q.returncode,classifier_stdout=q.stdout.decode('ascii') if q.stdout in [b'',b'running\n',b'terminal\n',b'pending\n'] else '<unrecognized>',filtered_bytes=len(f.stdout),filtered_sha256=hashlib.sha256(f.stdout).hexdigest())
  allowed={b'ActiveState',b'Job',b'LoadState',b'SubState',b'Result',b'ExecMainCode',b'ExecMainStatus'}
  safe=[]
  for line in p.stdout.splitlines():
   k,sep,val=line.partition(b'=')
   if sep and k in allowed and len(val)<=80 and all(32<=x<=126 for x in val):safe.append(line.decode('ascii'))
  if len(safe)==len(p.stdout.splitlines()):record['safe_property_lines']=safe
except subprocess.TimeoutExpired:record.update(ended_at=now(),timeout=True)
(O/'observation.json').write_text(json.dumps(record,indent=2)+'\n');print(json.dumps(record,indent=2))
