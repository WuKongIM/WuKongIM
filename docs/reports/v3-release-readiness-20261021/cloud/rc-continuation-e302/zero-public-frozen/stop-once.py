from pathlib import Path
import datetime,json,subprocess,hashlib
D=Path(__file__).parent;O=D/'stop';O.mkdir();C='e30299fa59ea6669ea71024eabe209840f241f19'
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
for n,endpoint in [('main','commits/main'),('workflow','actions/workflows/chat-lifecycle-stop.yml')]:
 p=subprocess.run(['gh','api','repos/WuKongIM/WuKongIM/'+endpoint],capture_output=True,timeout=60);assert p.returncode==0,'read-only stop guard failed';v=json.loads(p.stdout);(O/(n+'.json')).write_bytes(p.stdout)
 if n=='main':assert v['sha']==C
 else:assert v['state']=='active'
raw=subprocess.check_output(['git','show',C+':.github/workflows/chat-lifecycle-stop.yml']);(O/'workflow-source.yml').write_bytes(raw)
inputs={'request_id':'chat-20261021T095421Z-cf11d65c','stop_authorization':'operator-stop-chat-lifecycle'};(O/'inputs.json').write_text(json.dumps(inputs,indent=2)+'\n');cmd=['gh','workflow','run','chat-lifecycle-stop.yml','--repo','WuKongIM/WuKongIM','--ref','main','--json'];out=O/'dispatch-receipt.json';assert not out.exists();r={'schema':'wukongim.single_cleanup_dispatch/v1','started_at':now(),'command':cmd,'inputs':inputs,'control_sha_expected':C,'workflow_source_sha256':hashlib.sha256(raw).hexdigest(),'authority':'Original authorized final cleanup; parent explicitly instructed immediate existing Stop/Release after bounded diagnostic on same exact request','outcome':'pending_unknown_do_not_repeat'};out.write_text(json.dumps(r,indent=2)+'\n')
try:
 p=subprocess.run(cmd,input=json.dumps(inputs).encode(),capture_output=True,timeout=60);r.update(ended_at=now(),exit_code=p.returncode,stdout=p.stdout.decode(),stderr=p.stderr.decode(),outcome='accepted_reconcile_exact_run' if p.returncode==0 else 'nonzero_reconcile_before_any_retry')
except subprocess.TimeoutExpired:r.update(ended_at=now(),outcome='timeout_unknown_reconcile_no_repeat')
out.write_text(json.dumps(r,indent=2)+'\n');print(json.dumps(r,indent=2))
