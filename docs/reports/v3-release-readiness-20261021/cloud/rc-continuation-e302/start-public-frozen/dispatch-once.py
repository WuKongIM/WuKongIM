from pathlib import Path
import datetime,json,subprocess
D=Path(__file__).parent;out=D/'dispatch-receipt.json';assert not out.exists(),'Dispatch receipt already exists: reconcile, never retry'
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
assert json.loads((D/'fresh-guard-summary.json').read_text())['ready_for_single_dispatch']
cmd=['gh','workflow','run','chat-lifecycle-rehearsal.yml','--repo','WuKongIM/WuKongIM','--ref','main','--json']
r={'schema':'wukongim.single_dispatch_receipt/v1','started_at':now(),'command':cmd,'stdin_file':'planned-workflow-inputs.json','control_expected':'e30299fa59ea6669ea71024eabe209840f241f19','expected_run_number':33,'expected_attempt':1,'outcome':'pending_unknown_do_not_repeat'}
out.write_text(json.dumps(r,indent=2)+'\n')
try:
 p=subprocess.run(cmd,input=(D/'planned-workflow-inputs.json').read_bytes(),capture_output=True,timeout=60)
 r.update(ended_at=now(),exit_code=p.returncode,stdout=p.stdout.decode(),stderr=p.stderr.decode(),outcome='accepted_requires_run_reconciliation' if p.returncode==0 else 'nonzero_requires_run_reconciliation_no_retry')
except subprocess.TimeoutExpired:r.update(ended_at=now(),outcome='timeout_unknown_reconcile_no_retry')
out.write_text(json.dumps(r,indent=2)+'\n');print(json.dumps(r,indent=2))
