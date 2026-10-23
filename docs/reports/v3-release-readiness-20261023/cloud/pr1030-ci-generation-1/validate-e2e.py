#!/usr/bin/env python3
import pathlib,json,zipfile,hashlib,re
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1030-ci-generation-1'); aid=next((R/'PRIVATE_original-downloads').glob('*.zip')).stem;z=zipfile.ZipFile(R/'PRIVATE_original-downloads'/(aid+'.zip'))
expected={'binary-build-info.txt','binary.sha256','go-version.txt','identity.json'}|{f'{c}/{n}' for c in ['build','ordinary_history','cross_node_delivery','self_chat'] for n in ['command.json','result.json','stderr.log','stdout.jsonl']}|{'self_chat/report.json'}
assert set(z.namelist())==expected
report_body=z.read('self_chat/report.json');report=json.loads(report_body);assert report['passed'] is True and report['hash_slots']==256 and report['token_authentication']=='production_default'
assert {c['nodes'] for c in report['cases']}=={1,3}
ident=json.loads(z.read('identity.json'));assert ident['source_sha']=='a514c73b4c950b9ead150c28ff08ef89123f5238' and ident['policy_sha']=='27fa6d63cf3b2b6c3a9a28575f917d60cc054ded' and ident['run_id']=='39869765472' and ident['run_attempt']=='1'
binary=z.read('binary.sha256').decode().split()[0];assert re.fullmatch('[0-9a-f]{64}',binary)
out={'identity':ident,'binary_sha256':binary,'cases':{}}
commands={
 'build':(720,['go','build','-tags=e2e','-o','/home/runner/work/_temp/v3-candidate-39869765472-1/e2e/wukongim-e2e','./cmd/wukongim']),
 'ordinary_history':(780,['go','test','-tags=e2e','./test/e2e/migration/ordinary_history','-count=1','-timeout=12m','-p=1','-parallel=1','-json']),
 'cross_node_delivery':(180,['go','test','-tags=e2e','./test/e2e/message/cross_node_delivery','-count=1','-timeout=2m','-p=1','-json']),
 'self_chat':(300,['go','test','-tags=e2e','./test/e2e/message/self_chat','-count=1','-timeout=4m','-p=1','-json'])}
for case in ['build','ordinary_history','cross_node_delivery','self_chat']:
 command=json.loads(z.read(case+'/command.json'));timeout,args=commands[case]
 assert command['arguments']==args and command['timeout_seconds']==timeout and command['max_output_bytes']==64<<20
 assert command['working_dir']=='/home/runner/work/WuKongIM/WuKongIM'
 result=json.loads(z.read(case+'/result.json'));assert result['passed'] is True and result['status']=='passed' and result['exit_code']==0 and result['wrapper_exit_code']==0 and result['output_truncated'] is False
 assert result['elapsed_seconds']<timeout and result['output_bytes']<command['max_output_bytes']
 clean=result['cleanup'];assert clean['complete'] is True and clean['verified'] is True and clean['remaining_processes']==[]
 events=[json.loads(l) for l in z.read(case+'/stdout.jsonl').decode().splitlines() if l.startswith('{')]
 passed=[{k:e[k] for k in ['Time','Test','Elapsed'] if k in e} for e in events if e.get('Action')=='pass' and e.get('Test')]
 skips=[e for e in events if e.get('Action')=='skip'];fails=[e for e in events if e.get('Action')=='fail'];assert not fails
 if case=='cross_node_delivery':assert len(skips)==1 and skips[0]['Test']=='TestThreeNodeReplicaLoadComparisonAtTwoThousandQPS'
 else:assert not skips
 out['cases'][case]={'command':command,'result':result,'test_pass_events':passed,'skip_events':skips,'skip_output':[e for e in events if e.get('Action')=='output' and e.get('Test') in {s['Test'] for s in skips}],'fail_events':fails}
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
