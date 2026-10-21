#!/usr/bin/env python3
import argparse,hashlib,json,zipfile
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--root',type=Path,required=True);p.add_argument('--output-dir',type=Path,required=True);a=p.parse_args();r=a.root
prefix='attempt-1/acquire-artifact/cloud-lease-provision-chat-20261021T095421Z-cf11d65c/'
dep='attempt-1/deployment-1-artifact/cloud-deployment-39582829931/'
allowed={prefix+'quote.json',prefix+'receipt.json','attempt-1/attempted-deployment-control-shas','attempt-1/attempted-repair-source-shas','attempt-1/bootstrap-access.json',*[dep+n for n in ['analysis-endpoint.json','deployment-outcome.json','deployment-plan.json','encrypted-access.json','readiness-snapshot.json']],'attempt-1/deployment-plan.json','attempt-1/deployment-ssh-config','attempt-1/deployment-ssh-config.known_hosts','attempt-1/lease-plan.json','attempt-1/preflight-quote.json','attempt-1/quote-artifact/cloud-lease-provision-chat-20261021T095421Z-cf11d65c/quote.json','attempt-1/quote.json','attempt-1/receipt-selector.json','attempt-1/receipt.json','attempt-1/release-selector.json','attempt-1/run-plan.json','cleanup.json','deployment-repair-pending.json','release-1-1/cloud-lease-release-chat-20261021T095421Z-cf11d65c/release.json','release-selector.json','zero-inventory.json'}
records=[];collections=[]
for artifact,zname in [(12338781445,'cleanup.zip'),(12339131183,'handoff-named.zip')]:
 m=json.loads((r/f'artifact-{artifact}.json').read_bytes());raw=(r/zname).read_bytes();assert len(raw)==m['size_in_bytes'];assert 'sha256:'+hashlib.sha256(raw).hexdigest()==m['digest'];assert not m['expired'];data={}
 with zipfile.ZipFile(r/zname) as z:
  assert set(z.namelist())==allowed;assert len(z.namelist())==len(allowed);assert z.testzip() is None
  for info in z.infolist():
   assert info.file_size<100000; b=z.read(info);assert b'PRIVATE KEY' not in b
   if info.filename.endswith('.json'):json.loads(b)
   data[info.filename]=b
 collections.append(data);records.append({'artifact_id':artifact,'bytes':len(raw),'zip_sha256':hashlib.sha256(raw).hexdigest(),'zip_crc_pass':True,'members':[{'path':n,'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest()} for n,b in sorted(data.items())]})
assert collections[0]==collections[1];data=collections[0]
expected=json.loads((r.parent/'acquire-public-frozen/expected-selector.json').read_bytes())['selector'];acquired=json.loads((r.parent/'acquire/verified/receipt.json').read_bytes())['receipt'];released=json.loads((r.parent/'release/verified/release.json').read_bytes())
assert data['attempt-1/receipt.json']==(r.parent/'acquire/verified/receipt.json').read_bytes()
assert data['zero-inventory.json']==(r.parent/'release/verified/release.json').read_bytes()
zero=released['result']['zero_inventory'];assert zero['selector']==expected;assert zero['account_id_hash']==acquired['account_id_hash'];assert 'receipt' not in released['result']
assert zero['scopes']==['disk_attachments','disks','eip_associations','eips','enis','instances','nat_gateways','route_entries','security_group_rules','security_groups','vpcs','vswitches']
assert zero['observed_at']>acquired['created_at']
for n in ['release-selector.json','attempt-1/receipt-selector.json','attempt-1/release-selector.json']:assert json.loads(data[n])['selector']==expected
for n in ['attempt-1/attempted-deployment-control-shas','attempt-1/attempted-repair-source-shas']:assert data[n]==b'fc18221282f289088ec9b4282fda8e0bfc67a3b8\n'
assert json.loads(data['cleanup.json'])['zero_inventory'] is True
assert 'handoff.json' not in data and 'run-start.json' not in data and 'pre-clock-terminal.json' not in data
excluded=['attempt-1/deployment-ssh-config','attempt-1/deployment-ssh-config.known_hosts']
a.output_dir.mkdir()
for n,b in data.items():
 if n in excluded:continue
 f=a.output_dir/n;f.parent.mkdir(parents=True,exist_ok=True);f.write_bytes(b)
out={'artifacts':records,'two_artifact_members_byte_identical':True,'exact_acquire_receipt_and_release_bytes':True,'zero_selector_and_account_match':True,'zero_scopes_match_frozen_provider_12_scopes':True,'zero_observed_after_acquire':True,'only_source_and_control_fc182':True,'no_measured_run_start_or_handoff_receipt':True,'coverage_gap':['no pre-clock service state','no pre-clock journal or terminal summary','no historical Issue search response'],'not_archived_members':excluded,'not_archived_reason':'SSH configuration contains only runner key paths/host trust, unnecessary for public failure diagnosis; metadata hash retained.','no_private_decryption_or_payload_execution':True}
(a.output_dir/'verification.json').write_text(json.dumps(out,indent=2)+'\n');print(json.dumps({k:v for k,v in out.items() if k!='artifacts'}))
