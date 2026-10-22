#!/usr/bin/env python3
"""Read-only exact deployment artifact selection; encrypted envelopes stay private."""
from pathlib import Path
import json,hashlib,zipfile,stat
D=Path(__file__).parent;P=D.parent.parent/'pr1024-authorized-run-resume-20261023-private/live-observation'
m=json.loads((D/'deployment/artifact-metadata.json').read_text());raw=(P/f'deployment-{m["id"]}.zip').read_bytes()
assert len(raw)==m['size_in_bytes']<2*1024*1024 and 'sha256:'+hashlib.sha256(raw).hexdigest()==m['digest'] and not m['expired']
assert m['workflow_run']['id']==39822605006 and m['workflow_run']['head_sha']=='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06' and m['workflow_run']['head_branch']=='main'
selected={'deployment-plan.json','readiness-snapshot.json','deployment-outcome.json','analysis-endpoint.json'}
expected=selected|{'encrypted-access.json','encrypted-deployment-identity.json'}
def pairs(xs):
 d={}
 for k,v in xs:assert k not in d;d[k]=v
 return d
out=D/'deployment/verified';out.mkdir();rows=[]
with zipfile.ZipFile(P/f'deployment-{m["id"]}.zip') as z:
 assert len(z.namelist())==len(set(z.namelist())) and set(z.namelist())==expected
 for i in z.infolist():
  assert not i.is_dir() and stat.S_IFMT(i.external_attr>>16) in (0,stat.S_IFREG) and i.file_size<=1024*1024
  b=z.read(i);assert len(b)==i.file_size and b'-----BEGIN' not in b
  v=json.loads(b,object_pairs_hook=pairs);assert isinstance(v,dict)
  rows.append({'name':i.filename,'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest(),'schema':v.get('schema'),'public_payload_copied':i.filename in selected})
  if i.filename in selected:(out/i.filename).write_bytes(b)
 assert z.testzip() is None
(out/'archive-verification.json').write_text(json.dumps({'artifact_id':m['id'],'api_sha_size_and_zip_crc_verified':True,'members':rows,'encrypted_payloads_retained_only_in_private_zip':True},indent=2)+'\n')
print(json.dumps({'artifact_id':m['id'],'members':len(rows),'public_payloads':len(selected),'payload_execution':False}))
