#!/usr/bin/env python3
"""Offline verification of one exact continuation's four terminal public artifacts."""
import argparse,hashlib,json,pathlib,stat,zipfile
P=argparse.ArgumentParser(); P.add_argument('--root',type=pathlib.Path,required=True); a=P.parse_args(); root=a.root; t=root/'terminal'
REQ='chat-20261021T095421Z-cf11d65c'; CONTROL='e30299fa59ea6669ea71024eabe209840f241f19'; SOURCE='fc18221282f289088ec9b4282fda8e0bfc67a3b8'; RUN=39605452697
BUNDLE='sha256:e810a871dbf2d97fb7b30adba60470461081a3439c3bd7c833e67b34c8f56ca8'
def sha(b): return hashlib.sha256(b).hexdigest()
def pairs(p):
 d={}
 for k,v in p:
  if k in d: raise ValueError('duplicate JSON key')
  d[k]=v
 return d
def j(b): return json.loads(b,object_pairs_hook=pairs)
def write(p,b):
 p.parent.mkdir(parents=True,exist_ok=True)
 if p.exists(): assert p.read_bytes()==b, str(p)+' differs'
 else: p.write_bytes(b)
ids=[12350665875,12350640944,12350665865,12350421112]; archives={}; inventory={}
for aid in ids:
 meta=j((t/f'artifact-{aid}.json').read_bytes()); data=(t/f'{aid}.zip').read_bytes()
 assert meta['id']==aid and len(data)==meta['size_in_bytes'] and 'sha256:'+sha(data)==meta['digest']
 r=meta['workflow_run']; assert r['id']==RUN and r['repository_id']==638054601 and r['head_repository_id']==638054601 and r['head_branch']=='main' and r['head_sha']==CONTROL
 assert not meta['expired'] and len(data)<16*1024*1024
 with zipfile.ZipFile(t/f'{aid}.zip') as z:
  assert z.testzip() is None
  names=z.namelist(); assert len(names)<=128 and len(set(names))==len(names)
  content={}
  for info in z.infolist():
   path=pathlib.PurePosixPath(info.filename); assert not path.is_absolute() and '..' not in path.parts and not info.is_dir() and info.file_size<=2*1024*1024
   assert not stat.S_ISLNK(info.external_attr>>16)
   b=z.read(info); content[info.filename]=b
   assert b'PRIVATE KEY-----' not in b
   if info.filename.endswith('.json'): j(b)
  archives[aid]=content
  inventory[str(aid)]={'name':meta['name'],'zip_sha256':sha(data),'zip_bytes':len(data),'members':{n:{'sha256':sha(b),'bytes':len(b)} for n,b in sorted(content.items())}}
main=archives[12350665875]; assert len(main)==55 and main==archives[12350640944]
assert archives[12350665865]=={'issue-preflight.json':main['issue-preflight.json']}
assert archives[12350421112]=={'continuation-authentication.json':main['continuation-authentication.json']}
assert not any(pathlib.PurePosixPath(n).name in ['handoff.json','run-start.json','pre-clock-terminal.json'] for n in main)
assert j(main['issue-preflight.json'])['status']=='ready'; assert j(main['issue-preflight.json'])['issue_number']==1018
assert j(main['continuation-authentication.json'])['status']=='ready'
assert main['attempt-2/attempted-repair-source-shas'].decode().strip()==SOURCE
assert main['attempt-2/attempted-deployment-control-shas'].decode().strip()==CONTROL
for member,local in [('attempt-2/receipt.json','acquire/verified/receipt.json'),('attempt-2/quote.json','quote/verified/quote.json'),('zero-inventory.json','release/verified/release.json')]:
 assert main[member]==(root/local).read_bytes(),member
for member in ['attempt-2/acquire-artifact/cloud-lease-provision-'+REQ+'/receipt.json']:
 assert main[member]==main['attempt-2/receipt.json']
for member in ['attempt-2/acquire-artifact/cloud-lease-provision-'+REQ+'/quote.json','attempt-2/quote-artifact/cloud-lease-provision-'+REQ+'/quote.json']:
 assert main[member]==main['attempt-2/quote.json']
assert main['release-1-1/cloud-lease-release-'+REQ+'/release.json']==main['zero-inventory.json']
proof=j(main['continuation/proof.json']); manifestbytes=main['continuation/evidence-manifest.json']; manifest=j(manifestbytes)
assert proof['evidence_manifest_sha256']==sha(manifestbytes) and len(manifest['files'])==18
assert manifest['schema']=='wukongim.chat_lifecycle.released_preclock_evidence/v1'
for n,entry in manifest['files'].items():
 b=main['continuation/evidence/'+n]; assert entry=={'sha256':sha(b),'size':len(b)}
expect={'schema':'wukongim.chat_lifecycle.released_preclock_continuation/v1','request_id':REQ,'source_sha':SOURCE,'control_sha':CONTROL,'current_run_id':RUN,'current_run_number':33,'current_run_attempt':1,'current_workflow_id':329938003,'predecessor_run_id':39581355444,'predecessor_run_number':32,'predecessor_run_attempt':1,'predecessor_workflow_id':329938003,'cost_method':'full_admitted_quote_reserve','committed_micros':74477600,'next_attempt':2,'failure_class':'unclassified_before_accepted_run_start'}
for k,v in expect.items(): assert proof[k]==v,(k,proof.get(k))
assert j(main['attempt-2/quote.json'])['quote']['estimated_cost_micros']==74477600
plan=j(main['attempt-2/run-plan.json']); assert plan['stage']=='rehearsal' and plan['attempt']==2 and plan['workload_duration_seconds']==7200
assert plan['lease_plan']['provenance']=={'source_sha':SOURCE,'bundle_digest':BUNDLE}
assert plan['lease_plan']['budget']=={'currency':'CNY','limit_micros':1500000000,'committed_micros':74477600,'operational_stop_micros':1350000000}
assert plan['lease_plan']['lease_id']==REQ+'-rehearsal-2' and plan['lease_plan']['request_id']==REQ
zero=j(main['zero-inventory.json'])['result']['zero_inventory']; assert zero['selector']['lease_id']==REQ+'-rehearsal-2' and zero['selector']['request_id']==REQ and zero['selector']['plan_digest']=='e75273c61c954136f5c146aa62ced1b5ba6e2889090e007f553d2330535473e6'
assert zero['scopes']==['disk_attachments','disks','eip_associations','eips','enis','instances','nat_gateways','route_entries','security_group_rules','security_groups','vpcs','vswitches']
clean=j(main['cleanup.json']); assert clean['zero_inventory'] is True and int(clean['release_run_id'])==39609951023
prefix='attempt-2/pre-clock-generation-1/'
diag=j(main[prefix+'diagnostic.json']); unit=j(main[prefix+'unit.json']); assert diag['unit']==unit
assert diag['source_sha']==SOURCE and diag['control_sha']==CONTROL and diag['bundle_digest']==BUNDLE
assert diag['before_kill'] is True and diag['branch']=='stage_state_parse_failed'
assert [diag[k] for k in ['start_command_exit','state_command_exit','state_classifier_exit']]==[0,0,1]
assert diag['observed_epoch']-diag['readiness_started_epoch']==7 and diag['deadline_checked_epoch'] is None
assert unit['invalid_fields']==['Job'] and unit['missing_fields']==[] and unit['field_sha256']['Job']==sha(b'')
assert unit['properties']['ActiveState']=='active' and unit['properties']['SubState']=='running'
raw=(t/'original-state-safe-reconstruction.txt').read_bytes(); assert len(raw)==unit['bytes']==104 and sha(raw)==unit['sha256']
top=j((t/'top-final.json').read_bytes()); assert top['id']==RUN and top['head_sha']==CONTROL and top['run_attempt']==1 and top['status']=='completed' and top['conclusion']=='failure'
excluded={'attempt-2/deployment-ssh-config','attempt-2/deployment-ssh-config.known_hosts'}
for n,b in main.items():
 if n not in excluded: write(t/'verified'/n,b)
write(t/'member-inventory.json',(json.dumps(inventory,indent=2,sort_keys=True)+'\n').encode())
result={'schema':'wukongim.rc_continuation_terminal_verification/v1','pass':True,'top_run_id':RUN,'top_conclusion':'failure','control_sha':CONTROL,'source_sha':SOURCE,'request_id':REQ,'terminal_artifacts':ids,'cleanup_handoff_named_members_identical':True,'artifact_names_do_not_prove_handoff':True,'accepted_run_start_evidence_present':False,'formal_qualification_started':False,'continuation_evidence_files_verified':18,'rehearsal_attempt':2,'original_reserve_micros':74477600,'current_quote_micros':74477600,'combined_quote_reserve_micros':148955200,'actual_bills':'unknown','diagnostic':{'branch':diag['branch'],'before_kill':True,'start_exit':0,'state_read_exit':0,'classifier_exit':1,'unit_state':'active/running','job_value':'empty','elapsed_after_start_seconds':7,'safe_original_state_sha256':sha(raw)},'release_run_id':39609951023,'zero_inventory':zero,'excluded_public_archive_members':sorted(excluded),'limits':['Original first-attempt cause remains unknown.','No workload run-start was accepted; no 2h/72h/capacity/recovery pass.','This offline verifier checks retained API artifact bytes and identity closure; no new live OIDC/provider call.','No bill amount or provider settlement is established by Quote reserves.']}
write(t/'verification.json',(json.dumps(result,indent=2,sort_keys=True)+'\n').encode()); print(json.dumps({'pass':True,'artifacts':4,'members':55,'continuation_evidence_files':18,'public_members':53}))
