#!/usr/bin/env python3
"""One new request-scoped diagnostic key and reviewable exact Issue body. No cloud calls."""
import datetime,hashlib,json,os,stat,subprocess
from pathlib import Path
D=Path(__file__).parent
A=json.loads((D.parent/'pr1024-authorized-run-v1/authorization.json').read_text());G=json.loads((D/'guard/fresh-public-guard.json').read_text())
assert G['guard_pass'] and G['request_id']==A['request_id']
request=A['request_id'];source=A['source_sha']
base=Path('/Users/tt/.wukongim-cloud-rc-identities')
if not base.exists():base.mkdir(mode=0o700)
s=base.lstat();assert stat.S_ISDIR(s.st_mode) and not base.is_symlink() and stat.S_IMODE(s.st_mode)==0o700 and s.st_uid==os.getuid(),'identity parent is not owned 0700 directory'
root=base/request;root.mkdir(mode=0o700,exist_ok=False)
key=root/'diagnostic_ed25519'
cmd=['ssh-keygen','-q','-t','ed25519','-N','','-C',request,'-f',str(key)]
r=subprocess.run(cmd,capture_output=True,timeout=30)
if r.returncode:
 p=D.parent/'pr1024-authorized-run-resume-20261023-private/keygen.stderr';p.write_bytes(r.stderr);p.chmod(0o600)
 raise RuntimeError('key generation failed; same request must be inspected, never automatically regenerated')
pub=Path(str(key)+'.pub');key.chmod(0o600);pub.chmod(0o600)
public=pub.read_text().strip()
derived=subprocess.run(['ssh-keygen','-y','-f',str(key)],capture_output=True,timeout=10,check=True).stdout.decode().strip()
assert public.split()[:2]==derived.split()[:2] and public.startswith('ssh-ed25519 ')
fingerprint=subprocess.run(['ssh-keygen','-lf',str(pub)],capture_output=True,timeout=10,check=True).stdout.decode().strip()
assert stat.S_IMODE(root.stat().st_mode)==0o700 and stat.S_IMODE(key.stat().st_mode)==0o600
identity={'request_id':request,'algorithm':'ed25519','private_path':str(key),'directory_mode':'0700','private_mode':'0600','public_mode':'0600','public_key':public,'fingerprint':fingerprint,'public_private_match':True,'generated_once':True,'private_key_archived':False}
(D/'local-diagnostic-identity.json').write_text(json.dumps(identity,indent=2)+'\n')
(D/'diagnostic.pub').write_text(public+'\n')
body=f'''This is the unique tracking Issue for authorized cloud RC request `{request}`. Preparation is complete; the paid workflow has **not been dispatched** at Issue creation. Runtime comments will record its actual state.

Authorization: the operator explicitly answered “授权按此方案启动新的云 RC” for the following exact scope. Operator: `tangtaoit`.

- Product and control source: [`{source}`](https://github.com/WuKongIM/WuKongIM/commit/{source}); tree `{G['tree']}`. The frozen merged-source plan binds 24 rule/source files to this revision.
- New request, first rehearsal attempt; `released_preclock_run_id` is empty. No predecessor request, stopped request, Lease or marker is reused.
- Hangzhou: four hosts, each 4 vCPU / 8 GiB; three server nodes plus one load host, 256 hash slots. Temporary public SSH and HTTP endpoints are included in this authorization.
- Run sequence: 2-hour rehearsal followed by release and verified zero inventory; then a **new Lease** for a continuous 72-hour soak, at most 8 hours of capacity validation, and 30 minutes of recovery validation. The formal Lease's 96-hour limit is a resource deadline, not a 96-hour soak requirement.
- New request hard cap `1276567200` CNY micros (¥1,276.5672); operational stop `1126567200` CNY micros (¥1,126.5672). The previous three Quote reserves total `223432800` CNY micros (¥223.4328). They remain reserved outside this new request and are **not a provider invoice or actual-spend statement**. Aggregate hard/stop budgets remain ¥1,500 / ¥1,350; this request adds no budget.
- Live inventory, identity, capacity, Quote and budget admission are performed by the existing exact-source workflow. Any admission failure stops startup; no automatic budget increase, IAM/permission repair, or additional paid request is authorized here.
- Existing stage owners arm the required cleanup/finalizer schedules before their paid stages. Every terminal path must retain diagnostics and release this request's exact resources until zero inventory is proven. Existing idle-disabled schedules are not manually enabled during this preparation.
- A request-scoped Ed25519 diagnostic identity was created locally with matching public/private material; only its public key is supplied to Actions. Private material is neither included in this Issue nor archived in public evidence.

The root task delegated `/root/cloud_rc_execute` as the sole executor of the one initial dispatch of `chat-lifecycle-rehearsal.yml` on `main`, with both product/control pinned to the revision above. This Issue grants no separate dispatch, provider mutation, stable-channel publication or release approval. The existing authenticated scheduled continuation owns later stages.
'''
(D/'issue-body.md').write_text(body)
inputs={'source_sha':source,'operator':'tangtaoit','codex_diagnostic_pubkey':public,'request_id':request,'released_preclock_run_id':'','request_budget_limit_micros':'1276567200'}
assert len(inputs)==6
(D/'planned-workflow-inputs.json').write_text(json.dumps(inputs,indent=2)+'\n')
print(json.dumps({'request_id':request,'public_private_match':True,'fingerprint':fingerprint,'body_bytes':len(body.encode()),'six_inputs_prepared':True,'issue_created':False,'dispatched':False}))
