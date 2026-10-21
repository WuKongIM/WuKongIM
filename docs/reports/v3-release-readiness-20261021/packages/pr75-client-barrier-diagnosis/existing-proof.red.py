"""Real old guard probe; does not adapt its behavior or claim native execution."""
import importlib.util,json,sys,hashlib
from pathlib import Path
work=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-barrier-phase')
s=importlib.util.spec_from_file_location('t',work/'tests/test_native_stable_clients_drill.py');t=importlib.util.module_from_spec(s);s.loader.exec_module(t);test=t.NativeStableClientsTests();out=[]
for mutation in ('A-only-payload','A-only-mirror','payload-before-mirror','other-case','missing-serial','other-retained'):
 d,plan,observed,payload,alias=test.rpm_request_fixture()
 if mutation=='A-only-payload':observed[-1]['path']='/'+payload['object_key']
 elif mutation=='A-only-mirror':observed[-2]=dict(observed[0])
 elif mutation=='payload-before-mirror':observed[-2:]=list(reversed(observed[-2:]))
 elif mutation=='other-case':observed[-1]['serial']=2
 elif mutation=='missing-serial':observed[-1].pop('serial')
 else:
  plan['snapshots'].append({'channel':'stable','audit_release_id':4244,'candidate':{'snapshot_sha256':'c'*64}})
  prefix='/snapshots/stable/r4244-'+'c'*64+'/'
  alias['public_uri']=prefix+payload['source']['member'];alias['object_key']='public'+alias['public_uri'];observed[-1]['path']=alias['public_uri']
  body=(d.ORIGIN+prefix+'site/rpm/stable/el/9/x86_64/\n').encode();observed[-2].update(sha256=hashlib.sha256(body).hexdigest(),size=len(body))
 try:d.verify_rpm_requests(observed,plan,4242,switching=True);rejected=False
 except RuntimeError:rejected=True
 out.append({'mutation':mutation,'rejected':rejected})
print(json.dumps(out,indent=2));sys.exit(0 if all(r['rejected'] for r in out) else 1)
