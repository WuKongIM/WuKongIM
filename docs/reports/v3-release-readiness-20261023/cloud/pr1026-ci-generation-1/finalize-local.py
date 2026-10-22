#!/usr/bin/env python3
import hashlib,json,pathlib,re,zipfile
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1026-ci-generation-1')
sha=lambda b:hashlib.sha256(b).hexdigest()
head='23cf3f8764c2b841128b1a80d79ba9f49f447549';checkout='2f7e1df8492c98255092ae69c813def0c5703fe1';tree='89011038f444c5e9c8bd95f4a6814868676b4501'
poll=sorted((R/'polls').iterdir())[-1]
runs=[]
for rid in [39832747504,39832747798]:
 run=json.loads((poll/f'run-{rid}.json').read_text());jobs=json.loads((poll/f'jobs-{rid}.json').read_text())['jobs'];arts=json.loads((poll/f'artifacts-{rid}.json').read_text())['artifacts']
 assert run['run_attempt']==1 and run['head_sha']==head and run['status']=='completed' and run['conclusion']=='success'
 for j in jobs:
  assert j['status']=='completed' and j['conclusion'] in ['success','skipped'] and j['run_attempt']==1 and j['head_sha']==head
  if j['steps']:
   log=(R/'logs'/f"job-{j['id']}.log").read_bytes()
   found=re.findall(rb'git log -1 --format=%H\r?\n[^\n]*?([0-9a-f]{40})',log)
   if j['name']!='PR three-node regression':assert found==[checkout.encode()],(j['name'],found)
 runs.append({'id':rid,'conclusion':'success','attempt':1,'jobs':[{'id':j['id'],'name':j['name'],'conclusion':j['conclusion']} for j in jobs],'artifact_inventory_count':len(arts)})
commit=json.loads((R/'actual-checkout-commit.json').read_text());assert commit['sha']==checkout and commit['tree']['sha']==tree
assert json.loads((R/'head-commit.json').read_text())['tree']['sha']==tree
checks=json.loads((R/'final-pr-checks.json').read_text());assert len(checks)==13 and sum(x['state']=='SUCCESS' for x in checks)==11 and sum(x['state']=='SKIPPED' for x in checks)==2
assert len(json.loads((R/'final-head-runs.json').read_text())['workflow_runs'])==3
receipts=[]
for p in sorted((R/'verified-downloads').glob('*-receipt.json')):
 a=json.loads(p.read_text());b=(R/'verified-downloads'/(str(a['id'])+'.zip')).read_bytes();assert len(b)==a['zip_bytes'] and sha(b)==a['zip_sha256'];assert a['declared_digest']=='sha256:'+sha(b)
 with zipfile.ZipFile(R/'verified-downloads'/(str(a['id'])+'.zip')) as z:
  assert z.testzip() is None
  for m in a['members']:
   body=z.read(m['path']);assert sha(body)==m['sha256'] and len(body)==m['bytes'];assert (R/'artifacts'/a['name']/m['path']).read_bytes()==body
 receipts.append(a)
assert len(receipts)==6
for name in ['default','scripts']:
 d=R/'artifacts'/f'v3-candidate-{name}-39832747504-1';identity=json.loads((d/'identity.json').read_text());result=json.loads((d/'result.json').read_text());command=json.loads((d/'command.json').read_text())
 assert identity['source_sha']==checkout and identity['run_attempt']=='1' and identity['policy_sha']=='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06'
 assert sha((d/'policy.json').read_bytes())==identity['policy_sha256']=='2ee1b3162b431d77bec8cf68acc5b8aec0eede735f03c9f89d6c42e1d86cb0f9'
 assert result['passed'] and result['wrapper_exit_code']==0 and not result['output_truncated'];assert result['cleanup']['verified'] and result['cleanup']['complete'] and not result['cleanup']['remaining_processes']
 if name=='scripts':assert command['arguments']==['go','test','-tags=integration','./scripts/...','-count=1','-timeout=9m','-parallel=2'] and command['environment']=={'GOWORK':'off','GOFLAGS':'-p=2 -json'} and command['timeout_seconds']==600
script=json.loads((R/'scripts-integration-summary.json').read_text());assert any(x['event'].get('Action')=='pass' for x in script['target_events']) and not script['fail_events'];assert len(script['skip_events'])==6
e2e=json.loads((R/'e2e-summary.json').read_text());assert e2e['binary_sha256']=='d2b4dfb9a652aaffb69955b8d8306fc199dc90bac00e74f182e88c9a7e1a6a42'
assert all(x['result']['passed'] and x['result']['cleanup']['verified'] and x['result']['cleanup']['complete'] and not x['result']['cleanup']['remaining_processes'] for x in e2e['cases'].values())
perf={}
for d in sorted((R/'artifacts').glob('500qps-*')):
 assert (d/'source.sha').read_text().strip()==checkout and json.loads((d/'result.json').read_text())['status']=='pass'
 a=json.loads((d/'arrival.json').read_text());assert len(a['windows'])==3
 for w in a['windows']:assert w['planned']==w['started']==w['completed']==30000 and w['errors']==w['dropped']==0 and w['scheduled_to_completion_p99_ms']<400
 perf[d.name.split('-'+checkout)[0]]={'windows':a['windows']}
validation={'head':head,'actual_checkout':checkout,'tree':tree,'actual_checkout_tree_equals_reviewed_head':True,'runs':runs,'checks':{'success':11,'event_skipped':2,'total':13},'downloaded_artifacts':{'count':len(receipts),'total_zip_bytes':sum(x['zip_bytes'] for x in receipts),'all_downloaded_size_api_sha_crc_members_verified':True,'members':sum(len(x['members']) for x in receipts)},'excluded_large_artifact':{'id':12448689740,'declared_bytes':230287344,'state':'not_downloaded','bytes_size_sha_crc_not_verified':True,'reason':'larger than explicit 16 MiB small public evidence cap; success job log/API retained'},'default_suite':json.loads((R/'default-suite-log-summary.json').read_text()),'scripts':{'elapsed_seconds':script['result']['elapsed_seconds'],'test_pass_events':script['test_pass_events'],'package_pass_events':script['package_pass_events'],'skip_tests':[x['event']['Test'] for x in script['skip_events']],'target_go_test_elapsed_seconds':next(x['event']['Elapsed'] for x in script['target_events'] if x['event'].get('Action')=='pass'),'uploaded_python_23_80_detail_receipt':False},'e2e_case_elapsed_seconds':{k:v['result']['elapsed_seconds'] for k,v in e2e['cases'].items()},'e2e_skip_tests':[x['Test'] for v in e2e['cases'].values() for x in v['skip_events']],'performance':perf,'native_preview_triggered':False,'first_ci_failure':None,'local_artifact_parser_assertion_preserved':'e2e-validation-first-assertion-boundary.json','reruns_dispatches_mutations_merges_local_tests_performed':False,'qualification_limitations':['Python detailed 23/80 receipt consumed in temporary Go directory and not uploaded','Large three-node artifact not downloaded or byte-verified','Native package live/systemd/signed-repository tests skipped in scripts; native-preview not triggered','No 2000 QPS capacity, 2-hour rehearsal, 72-hour soak or message_loss root-cause qualification']}
(R/'validation.json').write_text(json.dumps(validation,indent=2)+'\n')
report='''# PR #1026 first Linux CI evidence

Head `23cf3f8764c2b841128b1a80d79ba9f49f447549`; actual PR merge checkout `2f7e1df8492c98255092ae69c813def0c5703fe1`; both trees equal `89011038f444c5e9c8bd95f4a6814868676b4501`. All non-aggregate executed job logs confirm that checkout. The merge parents are exact base `79a71770` and reviewed head `23cf3f87`.

Both acceptance runs finished successfully on attempt 1: Candidate 39832747504 has all three jobs successful; Three-Node 39832747798 has seven successful jobs and two event-skipped jobs (manual diagnosis and nightly qualification). Issue Signal 39832747456 succeeded but is not acceptance evidence. All 13 PR checks are terminal: 11 success and 2 skipped. No failed CI run, retry, dispatch, mutation, merge or local test was performed by this collector.

Candidate default passed 219 package summaries plus 15 no-test-file summaries. Individual default-test skip counts are not exposed by its non-verbose command. Scripts integration ran the unchanged frozen policy command once, in 575.414 seconds of its 600-second limit; output was not truncated and SID cleanup was verified/complete with no survivors. Its JSON stream contains 1,317 test-pass events, seven package-pass events, zero failure events and six explicit skips; these are event counts, including subtests. The exact terminal collector Go test passed in 27.57 seconds. Its Python 23-method/80-process-case detailed receipt was consumed from t.TempDir and not uploaded: this archive retains the actual Go pass event, not an invented Linux per-case receipt.

The native package lifecycle and signed repository tests were skipped by their existing opt-in environment gates. Two Docker tests, a scheduling subprocess fixture and a native deadline subprocess fixture were also skipped. Native-preview was not triggered. These checks cannot establish native package acceptance.

The E2E job built one binary (`d2b4dfb9...1a6a42`), then passed all five migration topologies (1→1, 1→3, 3→1, 3→3, 3→5), four cross-node functional tests, and self-chat with whole-cluster restart on one and three nodes. All four command/SID cleanup receipts are verified and complete with no survivors. The opt-in replica-load 2,000 QPS comparison was explicitly skipped; its reason is retained. An initial offline parser assertion incorrectly expected no E2E skips; that failure boundary and its corrected interpretation are preserved separately and are not a CI failure.

Each fresh performance seam passed three consecutive 60-second 500 QPS windows, each with 30,000 completions and zero errors/drops. Scheduled-to-completion P99 milliseconds: append 35.75/6.26/9.45; mixed SEND 12.71/14.98/15.33; TCP SENDACK 178.30/178.35/178.31; all below 400 ms. No baseline reuse or best-window selection is claimed.

Six small public artifact ZIPs (403,275 bytes total) were fully verified against API size/digest, all CRCs and member hashes, and extracted byte-identically. The 230,287,344-byte three-node correctness archive was not downloaded under the explicit 16 MiB small-evidence limit; its API metadata and complete successful job log remain available. Its bytes/CRC are unverified. No private cloud archive, key, identity envelope or production credential was downloaded.

This evidence qualifies the bounded PR regression scope. It does not qualify the failed cloud rehearsal, long soak, original message-loss root cause, production native release or 2,000 QPS capacity. `validation.json`, exact logs, command identities, artifacts and API snapshots carry the full details.
'''
(R/'README.md').write_text(report)
files=sorted(p for p in R.rglob('*') if p.is_file() and p.name not in ['PUBLIC_ALLOWLIST.txt','SHA256SUMS'])
allow=[str(p.relative_to(R)) for p in files]+['SHA256SUMS'];(R/'PUBLIC_ALLOWLIST.txt').write_text('\n'.join(sorted(allow))+'\n')
manifest_files=sorted(files+[R/'PUBLIC_ALLOWLIST.txt'])
(R/'SHA256SUMS').write_text(''.join(sha(p.read_bytes())+'  '+str(p.relative_to(R))+'\n' for p in manifest_files))
print(json.dumps({'files_hashed':len(manifest_files),'payload_bytes':sum(p.stat().st_size for p in manifest_files),'SHA256SUMS':sha((R/'SHA256SUMS').read_bytes()),'zip_count':len(receipts),'zip_bytes':sum(x['zip_bytes'] for x in receipts),'validation':str(R/'validation.json')}))
