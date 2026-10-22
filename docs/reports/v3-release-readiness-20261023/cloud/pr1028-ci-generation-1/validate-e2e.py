#!/usr/bin/env python3
import pathlib,json,zipfile,hashlib,re
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1028-ci-generation-1'); aid=next((R/'PRIVATE_original-downloads').glob('*.zip')).stem;z=zipfile.ZipFile(R/'PRIVATE_original-downloads'/(aid+'.zip'))
expected={'binary-build-info.txt','binary.sha256','go-version.txt','identity.json'}|{f'{c}/{n}' for c in ['build','ordinary_history','cross_node_delivery','self_chat'] for n in ['command.json','result.json','stderr.log','stdout.jsonl']}|{'self_chat/report.json'}
assert set(z.namelist())==expected
report_body=z.read('self_chat/report.json');report=json.loads(report_body);assert report['passed'] is True and report['hash_slots']==256 and report['token_authentication']=='production_default'
assert {c['nodes'] for c in report['cases']}=={1,3}
ident=json.loads(z.read('identity.json'));assert ident['source_sha']=='11e66c4e3f45a01750741e225f2feb246e60e8c0' and ident['policy_sha']=='2ab3e1a8bd95670603bc38314036c797a5124682' and ident['run_id']=='39854388522' and ident['run_attempt']=='1'
binary=z.read('binary.sha256').decode().split()[0];assert re.fullmatch('[0-9a-f]{64}',binary)
out={'identity':ident,'binary_sha256':binary,'cases':{}}
for case in ['build','ordinary_history','cross_node_delivery','self_chat']:
 result=json.loads(z.read(case+'/result.json'));assert result['passed'] is True and result['status']=='passed' and result['exit_code']==0 and result['wrapper_exit_code']==0 and result['output_truncated'] is False
 clean=result['cleanup'];assert clean['complete'] is True and clean['verified'] is True and clean['remaining_processes']==[]
 events=[json.loads(l) for l in z.read(case+'/stdout.jsonl').decode().splitlines() if l.startswith('{')]
 passed=[{k:e[k] for k in ['Time','Test','Elapsed'] if k in e} for e in events if e.get('Action')=='pass' and e.get('Test')]
 skips=[e for e in events if e.get('Action')=='skip'];fails=[e for e in events if e.get('Action')=='fail'];assert not fails
 if case=='cross_node_delivery':assert len(skips)==1 and skips[0]['Test']=='TestThreeNodeReplicaLoadComparisonAtTwoThousandQPS'
 else:assert not skips
 out['cases'][case]={'result':result,'test_pass_events':passed,'skip_events':skips,'skip_output':[e for e in events if e.get('Action')=='output' and e.get('Test') in {s['Test'] for s in skips}],'fail_events':fails}
assert len(out['cases']['ordinary_history']['test_pass_events'])==6
assert len(out['cases']['cross_node_delivery']['test_pass_events'])==4
assert len(out['cases']['self_chat']['test_pass_events'])==3
safe={'passed':report['passed'],'hash_slots':report['hash_slots'],'token_authentication':report['token_authentication'],'suppression_window_ms':report['suppression_window_ms'],'case_summary':[]}
for c in report['cases']:
 assert c['passed'] is True and c['binary_sha256']==binary and c['invalid_auth_refused'] is True and c['whole_cluster_restarts']==1
 safe['case_summary'].append({k:c[k] for k in ['nodes','passed','binary_sha256','invalid_auth_refused','whole_cluster_restarts','sender_suppression_windows']}|{'acknowledged_count':len(c['acknowledged']),'receives_count':len(c['receives']),'history_count':len(c['history'])})
# Identity/payload fields in the excluded report are never copied. Verify their string tokens are absent from every allowed text member.
sensitive=[]
def scan(x,key=''):
 if isinstance(x,dict):
  for k,v in x.items():scan(v,k)
 elif isinstance(x,list):
  for v in x:scan(v,key)
 elif key in {'uid','sender_uid','receiver_uid','channel_id','payload','from_uid','to_uid'} and isinstance(x,str) and len(x)>=6:sensitive.append(x.encode())
scan(report)
match=[]
for name in sorted(expected-{'self_chat/report.json'}):
 body=z.read(name)
 if any(t in body for t in sensitive):match.append(name)
 assert not match,match
 p=R/'verified-artifacts'/aid/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(body)
original=next(m for m in json.loads((R/'verified-downloads'/(aid+'-receipt.json')).read_text())['members'] if m['path']=='self_chat/report.json')
out['self_chat']=safe;out['excluded_private_original_report']={'artifact_id':int(aid),**original,'handling':'Original ZIP and raw identity report retained privately; only finite typed counts and booleans exposed. No uint64 conversion through floating point.'}
out['private_token_scan']={'tested_allowed_members':len(expected)-1,'excluded_report_identity_or_payload_string_token_count':len(sensitive),'matching_members':match}
(R/'e2e-summary.json').write_text(json.dumps(out,indent=2)+'\n');(R/'self-chat-safe-summary.json').write_text(json.dumps(safe,indent=2)+'\n')
print(json.dumps({'binary_sha256':binary,'case_test_pass_counts':{k:len(v['test_pass_events']) for k,v in out['cases'].items()},'skip_count':sum(len(v['skip_events']) for v in out['cases'].values()),'private_report_bytes':original['bytes'],'safe_case_counts':safe['case_summary'],'privacy_scan':out['private_token_scan']}))
