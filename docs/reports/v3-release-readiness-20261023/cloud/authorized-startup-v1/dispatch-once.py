#!/usr/bin/env python3
"""Single scoped initial dispatch; unknown outcome must be reconciled, never resent."""
from pathlib import Path
import subprocess,json,hashlib,datetime
D=Path(__file__).parent;P=D.parent/'pr1024-authorized-run-resume-20261023-private'
C='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06';request='chat-20261021T214505Z-00299e37'
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def sha(x):return hashlib.sha256(x).hexdigest()
def save(n,v):
 with (D/n).open('x') as f:json.dump(v,f,indent=2);f.write('\n')
def get(n,endpoint):
 r=subprocess.run(['gh','api','--method','GET',endpoint],capture_output=True,timeout=60,check=True);v=json.loads(r.stdout);save(n,{'observed_at':now(),'response_sha256':sha(r.stdout),'data':v});return v
assert json.loads((D/'guard/fresh-public-guard.json').read_text())['guard_pass']
assert json.loads((D/'issue-preflight.json').read_text())['status']=='ready'
assert json.loads((D/'prior-dispatch-reconciliation.json').read_text())['matching_runs']==[]
inputs=json.loads((D/'planned-workflow-inputs.json').read_text())
assert set(inputs)=={'source_sha','operator','codex_diagnostic_pubkey','request_id','released_preclock_run_id','request_budget_limit_micros'}
assert inputs['source_sha']==C and inputs['request_id']==request and inputs['operator']=='tangtaoit' and inputs['released_preclock_run_id']=='' and inputs['request_budget_limit_micros']=='1276567200'
issue=get('dispatch-issue-before.json','repos/WuKongIM/WuKongIM/issues/1025');assert issue['title']=='[chat-lifecycle] '+request and issue['state']=='open' and issue['locked'] is False
latest=get('dispatch-latest-top-before.json','repos/WuKongIM/WuKongIM/actions/workflows/chat-lifecycle-rehearsal.yml/runs?per_page=1')['workflow_runs'][0]
assert latest['id']==39637426411 and latest['status']=='completed' and latest['run_attempt']==1
main=get('dispatch-main-before.json','repos/WuKongIM/WuKongIM/git/ref/heads/main');assert main['object']['sha']==C
payload=json.dumps({'ref':'main','inputs':inputs}).encode()
save('dispatch-attempt.json',{'started_at':now(),'authorization_reference':'call_dd802aec589f4d058343640027bb7cf9','dispatch_owner':'/root/cloud_rc_execute','source_sha':C,'control_sha':C,'request_id':request,'workflow':'chat-lifecycle-rehearsal.yml','attempt':1,'payload_sha256':sha(payload),'issue':issue['html_url'],'request_hard_limit_micros':1276567200,'request_stop_micros':1126567200,'historical_quote_reserve_micros':223432800,'unknown_outcome_policy':'read reconciliation only; no automatic second dispatch'})
try:r=subprocess.run(['gh','api','--method','POST','repos/WuKongIM/WuKongIM/actions/workflows/chat-lifecycle-rehearsal.yml/dispatches','--input','-'],input=payload,capture_output=True,timeout=60)
except subprocess.TimeoutExpired as e:
 save('dispatch-result.json',{'observed_at':now(),'status':'unknown-timeout','stdout_sha256':sha(e.stdout or b''),'stderr_sha256':sha(e.stderr or b'')});raise RuntimeError('Unknown dispatch outcome; reconcile without retry')
(P/'dispatch.stderr').write_bytes(r.stderr)
save('dispatch-result.json',{'observed_at':now(),'exit_code':r.returncode,'stdout_bytes':len(r.stdout),'stdout_sha256':sha(r.stdout),'stderr_sha256':sha(r.stderr),'accepted':r.returncode==0,'run_id':None})
assert r.returncode==0,'dispatch outcome not accepted; no retry'
print(json.dumps({'dispatch_accepted':True,'request_id':request,'issue':issue['html_url'],'source_sha':C,'dispatch_count':1,'run_id_pending_read_reconciliation':True}))
