from pathlib import Path
import json,hashlib,collections,zipfile,re,datetime,subprocess
B=Path(__file__).parent;P=B.with_name(B.name+'-private');O=sorted((B/'polls').iterdir())[-1];H='2662fe93b754d614422f2936185cf781a33b831a';BASE='8dfcd35b7d604fb5d3da3d75e36ab267e2f4b1bf';M='5b3fef1c7a3418c13c4a4392533f112915d97ffe';TREE='1182b14d5b659eb60f2e7b694ff91b6641189106'
sha=lambda b:hashlib.sha256(b).hexdigest();load=lambda p:json.loads(p.read_text())
def save(n,v):
 data=(json.dumps(v,indent=2)+'\n').encode()
 if (B/n).exists():assert (B/n).read_bytes()==data;return
 with (B/n).open('xb') as f:f.write(data)
summary=load(O/'summary.json');assert summary['all_terminal'] and not summary['GET_failures']
runs=summary['runs'];assert {r['id'] for r in runs}=={39879047480,39879047447,39879047427,39879047433,39879055599}
assert all(r['head_sha']==H and r['run_attempt']==1 and r['event']=='pull_request' and r['conclusion']=='success' for r in runs)
jobs=[j for r in runs for j in r['jobs']];counts=dict(collections.Counter(j['conclusion'] for j in jobs));assert len(jobs)==19 and counts=={'success':17,'skipped':2}
pr=load(O/'pr.json');assert pr['head']['sha']==H and pr['base']['sha']==BASE and pr['draft'] is True and pr['merged'] is False
merge=load(O/'source-merge.json');assert merge=={'sha':M,'tree':TREE,'parents':[BASE,H]}
logs=[load(p) for p in sorted((B/'log-bindings').glob('*.json'))];assert len(logs)==17
for j in logs:
 expect=[] if j['name'] in ['Emit credential-free wake-up','PR three-node regression'] else [M];assert j['actual_checkout_candidates']==expect
 assert sha((P/'logs'/('job-'+str(j['id'])+'.log')).read_bytes())==j['sha256']
authority=load(B/'source-authority.json');assert all(x['unchanged'] for x in authority['files'])
W=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
for x in authority['files']:
 for rev,key in [(BASE,'base_sha256'),(H,'head_sha256')]:assert sha(subprocess.check_output(['git','show',rev+':'+x['path']],cwd=W))==x[key]
first=sorted((B/'polls').iterdir())[0];review=[load(p) for p in sorted(first.glob('workflow-*.json')) if not p.name.endswith('.read.json')];assert len(review)==3 and {x['state'] for x in review}=={'disabled_manually'}
named={k:load(B/(k+'-result.json')) for k in ['go-unit','scripts-integration']};default=load(B/'default-summary.json');scripts=load(B/'scripts-summary.json');e2e=load(B/'e2e-summary.json');migration=load(B/'migration-history-safe-summary.json');perf={k:load(B/('performance-'+k+'.json')) for k in ['tcp-sendack','channel-append','mixed-send']}
proofs=[load(p) for p in sorted((B/'proof-inventory').glob('*.json')) if not p.name.endswith('.read.json')];small=[a for a in proofs if a.get('state')=='verified_private_original'];large=[a for a in proofs if a.get('state')=='metadata_only'];assert len(small)==6 and len(large)==2 and sum(a['size_in_bytes'] for a in small)<=16<<20
assert all(m['utf8_text'] for a in small for m in a['members'])
cleanup=[{'check':n,**r['result']['cleanup']} for n,r in named.items()]+[{'check':n,**r['result']['cleanup']} for n,r in e2e['cases'].items()];assert len(cleanup)==6 and all(x['verified'] and x['complete'] and not x['remaining_processes'] for x in cleanup)
all_events=load(B/'scripts-safe-events.json');selected=[e for e in all_events if e.get('Action') in ['pass','skip','fail'] and (e.get('Test','') in ['TestChatLifecycleTerminalEvidenceRetention','TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire','TestLocalThresholdPprofInterruptJoinsRequestsAndWritesPartialMetadata'] or e.get('Test','').startswith('TestLocalThresholdPprofInterruptDuringRequestRegistration'))]
assert len(selected)==13 and all(x['Action']=='pass' for x in selected)
limits=['Default go-unit is nonverbose:219 package summaries do not independently enumerate every test case. Local2605/2321 counts are not substituted for Linux CI evidence.','Six scripts opt-in tests, two non-PR three-node jobs and one optional2000QPS comparison are explicitly skipped, not passes. Standalone EasySDK/Manager browser workflows were not triggered for this head.','Large correctness229226446B and native77899362B ZIPs are metadata-only. API declared digests are not claimed as locally recomputed full hashes; no large package binaries were downloaded.','Native Go wrapper suppresses successful inner systemd output. Evidence binds exact checkout, enabling env, actual lifecycle command, successful step and Go result; no independent container inventory receipt is invented.','Optional cosign verification of the GoReleaser tool was skipped because cosign was unavailable; download checksum and TEST ONLY package checks are separate. No production signing or stable native publication occurred.','TerminalEvidenceRetention parent PASS is retained; inner Python/process case receipts were not separately uploaded, so no per-case Linux receipt is invented.','CI acceptance for this diagnostic change does not resolve historical cloud failures, establish long-duration stability/capacity or qualify GA.']
value={'observed_at':summary['observed_at'],'source':{'head':H,'base':BASE,'actual_checkout':M,'tree':TREE,'exact_tree_equal':True,'merge_parents':[BASE,H],'actual_checkout_job_count':14},'runs':[{k:r[k] for k in ['id','name','event','run_attempt','status','conclusion','created_at','updated_at','html_url']} for r in runs],'job_conclusions':counts,'all_first_attempt':True,'two_natural_PR_signals_not_reruns':True,'webhook_action_for_second_signal_not_retained':True,'draft_at_final_read':True,'named_checks':named,'default_summary':{k:v for k,v in default.items() if k!='package_summaries'},'scripts_summary':scripts,'selected_scripts_regression_events':selected,'e2e':e2e,'migration_history_count':migration['history_verification_count'],'migration_temporary_retry_sum':migration['temporary_retry_sum'],'performance':perf,'owned_SID_cleanup':cleanup,'review_agent_workflows':review,'small_ZIP_count':len(small),'small_ZIP_bytes':sum(a['size_in_bytes'] for a in small),'small_ZIP_members':sum(a['member_count'] for a in small),'full_small_ZIP_size_SHA_CRC_UTF8_verified':True,'all_raw_ZIPs_and_logs_private':True,'metadata_only_artifacts':large,'limitations':limits,'local_observer_issue':'One collector filename glob included its own .read.json envelopes; failed before network; corrected locally and recorded in observer-error-1.json. No CI failure or rerun.','no_workflow_or_GitHub_mutation_source_execution_cloud_action':True,'all_owned_processes_terminal':True,'conclusion':'Final reviewed tree passes all applicable first-attempt automatic CI; explicit skips and evidence limits retained. Ready/merge is the parent owner decision.'}
save('validation.json',value)
readme=f'''# PR1031 final-head first automatic CI

The reviewed head `{H}` and actual checkout `{M}` share tree `{TREE}`. Checkout parents are base `{BASE}` and the exact head. Fourteen completed checkout jobs independently log that merge SHA. Frozen root rules, workflow contract and named-check policy are byte-identical between head/base and were rehashed after collection. No actual AGENTS/FLOW source file is in this public archive.

All five natural pull-request runs completed successfully on attempt1: candidate39879047480, three-node39879047447, native39879047427, and signals39879047433/39879055599. The second signal is a separate natural run; its precise webhook action was not retained. There are19 jobs:17 success and2 explicit non-PR branch skips. Draft status was unchanged at the final read. The three protected Review Agent workflows remained disabled in the recorded workflow-state snapshot.

Default go-unit passed448.344s:219 package passes and15 no-test-file packages. Scripts passed573.183s with651 root PASS/6 SKIP,1317 all-level PASS/6 SKIP,7 package PASS, zero FAIL and zero unfinished tests. Exact base-policy argv/cwd,1800s or600s outer timeout,8MiB output caps and GOFLAGS=-p=2 (scripts additionally -json) were verified. Scripts kept its original Go9m/-parallel2 limits. Test names were never filtered based on words such as Token/Secret/Authorization. Retention, the nine registration-signal cases, original interrupt and direct-lab Start all completed; nested79 process receipts are not separately present in the upload.

One fixed E2E binary `3d4f44652ad153ccfa6396d6a5729aa044f97c7845ff73b6d10aa0c9865cd270` passed all five migration topologies (1→1,1→3,3→1,3→3,3→5),55 logged history verifications with zero temporary retries, cross-node delivery, and self-chat on1/3 nodes with one whole-cluster restart each. Invalid auth refusal and sender suppression passed. Optional2000QPS comparison is an explicit skip. Build plus three suites and two named checks supply six verified/complete owned-SID cleanup receipts with empty remaining-process lists.

Each of three fresh500QPS seams completed three60s measured windows:270000 total measured planned/started/completed requests with zero errors/drops. The90000 warmup requests are separate. Window P99 maxima(ms):TCP178.061199,append12.705597,mixed16.869351, all below400ms. Actual counters,4-CPU Linux/Go1.25.11 host and source/binary identities are in the closed summaries. The correctness job separately logged75502 sent/acknowledged and expected operator_stop; its full artifact was not downloaded.

Native preview passed build/install, TEST ONLY signed-repository fixture and real-systemd lifecycle on Ubuntu24.04,Debian12,Rocky9,Alma9. Successful inner lifecycle stdout is suppressed by the existing Go wrapper; this audit binds enabled test, exact checkout, command and actual job results. No independent container zero-inventory claim or production publication is made. Optional GoReleaser cosign verification was unavailable.

Six small proof ZIPs total406527 bytes with80 actual UTF8 members: full API size/SHA, member CRC/path/type checks are verified. Every original ZIP/log, including self-chat raw identities, is in the private sibling and excluded. Only finite typed evidence and hashes are public. Correctness229226446B and native77899362B artifact bodies remain metadata-only. The superseded11de generation is separately retained: candidate cancelled, other three success; none substitutes for final-head evidence.

No dispatch, retry, cancellation, workflow enable, comment, ready/merge, source execution, Go test, provider call or cloud operation occurred. Passing this diagnostic PR does not establish GA, fix prior cloud failures, prove72-hour stability or publish the stable package channel. See validation.json for explicit skips and limits. All local subprocesses are terminal.
'''
(B/'README.md').write_text(readme)
# Closed groups created by this auditor only. No raw private response, ZIP or identities.
top=['README.md','validation.json','source-authority.json','collect-metadata.py','collect-completed.py','validate-small-proofs.py','summarize-job-logs.py','finalize.py','observer-error-1.json','observer-error-2.json','go-unit-result.json','scripts-integration-result.json','default-summary.json','scripts-summary.json','scripts-safe-events.json','performance-tcp-sendack.json','performance-channel-append.json','performance-mixed-send.json','e2e-summary.json','migration-history-safe-summary.json','safe-job-log-summary.json']
names=set(top)
for folder,pattern in [('polls','*/*.json'),('proof-inventory','*.json'),('log-bindings','*.json')]:names.update(p.relative_to(B).as_posix() for p in (B/folder).glob(pattern) if p.is_file())
private_report=json.loads(zipfile.ZipFile(P/'archives/12469194479.zip').read('self_chat/report.json'));sensitive=[]
def scan(v,key=''):
 if isinstance(v,dict):
  for k,x in v.items():scan(x,k)
 elif isinstance(v,list):
  for x in v:scan(x,key)
 elif key in ['uid','sender_uid','receiver_uid','channel_id','payload','from_uid','to_uid'] and isinstance(v,str) and len(v)>=6:sensitive.append(v.encode())
scan(private_report)
for n in names:
 assert not any(s in n for s in ['.zip','AGENTS.md','FLOW.md','self_chat/report.json'])
 raw=(B/n).read_bytes();raw.decode('utf-8');assert b'\0' not in raw and not any(s in raw for s in sensitive)
 assert not re.search(rb'-----BEGIN (?:OPENSSH |RSA |EC |PGP )?PRIVATE KEY',raw)
 assert not re.search(rb'https?://[^\s\"\']+[?&](?:X-Amz-Signature|X-Goog-Signature|sig)=[A-Za-z0-9%]{10,}',raw)
assert not {p.relative_to(B).as_posix() for p in B.glob('*.json')}-names
names=sorted(names);(B/'PUBLIC_ALLOWLIST.txt').write_text('\n'.join(names)+'\n');checksum_names=sorted(names+['PUBLIC_ALLOWLIST.txt']);(B/'SHA256SUMS').write_text(''.join(sha((B/n).read_bytes())+'  '+n+'\n' for n in checksum_names))
entries=[{'path':n,'bytes':(B/n).stat().st_size,'sha256':sha((B/n).read_bytes())} for n in sorted(checksum_names+['SHA256SUMS'])]
archive={'schema':'finite-public-ci-archive/v1','source_head':H,'payload_files':len(names),'payload_bytes':sum((B/n).stat().st_size for n in names),'sha256sums_sha256':sha((B/'SHA256SUMS').read_bytes()),'allowlist_sha256':sha((B/'PUBLIC_ALLOWLIST.txt').read_bytes()),'files':entries,'private_identity_token_scan':{'tokens':len(sensitive),'matches':0},'all_files_utf8':True,'all_owned_processes_terminal':True};save('archive-sha256.json',archive)
for x in entries:assert sha((B/x['path']).read_bytes())==x['sha256'] and (B/x['path']).stat().st_size==x['bytes']
print(json.dumps({'directory':str(B),'payload_files':len(names),'payload_bytes':archive['payload_bytes'],'manifest_entries':len(entries),'SHA256SUMS':archive['sha256sums_sha256'],'archive_sha256':sha((B/'archive-sha256.json').read_bytes()),'small_ZIP_bytes':406527,'check_conclusions':counts,'all_owned_processes_terminal':True}))
