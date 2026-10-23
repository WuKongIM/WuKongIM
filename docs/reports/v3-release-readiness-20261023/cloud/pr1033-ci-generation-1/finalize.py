from pathlib import Path
import json,hashlib,collections,zipfile,re,subprocess
B=Path(__file__).parent;P=B.with_name(B.name+'-private');O=sorted((B/'polls').iterdir())[-1]
H='4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac';BASE='9e5d8eeadb7b4c1998857640d2160c3af3232996';M='9e8332ed8ea69540e5f248c7e483716e987a2546';TREE='1f2826553a264053e954229ac2581bcf48afe6ad'
sha=lambda b:hashlib.sha256(b).hexdigest();load=lambda p:json.loads(p.read_text())
def save(n,v):
 data=(json.dumps(v,indent=2)+'\n').encode()
 if (B/n).exists():assert (B/n).read_bytes()==data;return
 with (B/n).open('xb') as f:f.write(data)
summary=load(O/'summary.json');assert summary['all_terminal'] and not summary['GET_failures']
runs=summary['runs'];assert {r['id'] for r in runs}=={39893010352,39893010250,39893010300,39893010410}
assert all(r['head_sha']==H and r['run_attempt']==1 and r['event']=='pull_request' and r['conclusion']=='success' for r in runs)
jobs=[j for r in runs for j in r['jobs']];counts=dict(collections.Counter(j['conclusion'] for j in jobs));assert len(jobs)==18 and counts=={'success':16,'skipped':2}
pr=load(O/'pr.json');assert pr['head']['sha']==H and pr['base']['sha']==BASE and pr['draft'] is False and pr['merged'] is False
merge=load(O/'source-merge.json');assert merge=={'sha':M,'tree':TREE,'parents':[BASE,H]}
for f in O.glob('artifacts-*.json'):
 if f.name.endswith('.read.json'):continue
 for a in load(f)['artifacts']:
  assert a['workflow_run']['head_sha']==H and a['workflow_run']['id'] in {r['id'] for r in runs}
logs=[load(p) for p in sorted((B/'log-bindings').glob('*.json'))];assert len(logs)==16
for j in logs:
 expect=[] if j['name'] in ['Emit credential-free wake-up','PR three-node regression'] else [M]
 assert j['actual_checkout_candidates']==expect and sha((P/'logs'/('job-'+str(j['id'])+'.log')).read_bytes())==j['sha256']
authority=load(B/'source-authority.json');assert all(x['unchanged'] for x in authority['files'])
W=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
primary_main=subprocess.check_output(['git','rev-parse','refs/heads/main'],cwd=W,text=True).strip()
assert primary_main=='a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83'
for x in authority['files']:
 for rev,key in [(BASE,'base_sha256'),(H,'head_sha256')]:assert sha(subprocess.check_output(['git','show',rev+':'+x['path']],cwd=W))==x[key]
first=sorted((B/'polls').iterdir())[0];review=[load(p) for p in sorted(first.glob('workflow-*.json')) if not p.name.endswith('.read.json')];assert len(review)==4 and {x['state'] for x in review}=={'disabled_manually'}
named={k:load(B/(k+'-result.json')) for k in ['go-unit','scripts-integration']};default=load(B/'default-summary.json');scripts=load(B/'scripts-summary.json');e2e=load(B/'e2e-summary.json');perf={k:load(B/('performance-'+k+'.json')) for k in ['tcp-sendack','channel-append','mixed-send']}
proofs=[load(p) for p in sorted((B/'proof-inventory').glob('*.json')) if not p.name.endswith('.read.json')];small=[a for a in proofs if a.get('state')=='verified_private_original'];large=[a for a in proofs if a.get('state')=='metadata_only'];assert len(small)==6 and len(large)==2 and sum(a['size_in_bytes'] for a in small)<=16<<20
assert all(m['utf8_text'] for a in small for m in a['members'])
a=next(x for x in small if x['name'].startswith('v3-candidate-e2e-'));z=zipfile.ZipFile(P/'archives'/(str(a['id'])+'.zip'));body=z.read('ordinary_history/stdout.jsonl');events=[json.loads(x) for x in body.decode().splitlines() if x.startswith('{')];history=[];topologies=[]
for e in events:
 text=e.get('Output','');m=re.search(r'history verified: phase=([a-z-]+) node=(\d+) rows=(\d+) temporary_retries=(\d+)',text)
 if m:
  phase,node,rows,retries=m.groups();assert phase in ['after-import','after-protocol-append','after-full-restart','after-leader-stop','final']
  history.append({'time':e['Time'],'test':e['Test'],'phase':phase,'node':int(node),'rows':int(rows),'temporary_retries':int(retries)})
 m=re.search(r'original (\d+)-node source -> (\d+)-node cluster: history, credentials, appends, idempotency, full restart and applicable leader failover passed',text)
 if m:topologies.append({'time':e['Time'],'test':e['Test'],'source_nodes':int(m[1]),'target_nodes':int(m[2]),'history_credentials_appends_idempotency_restart_applicable_failover_passed':True})
assert len(history)==55 and {(x['source_nodes'],x['target_nodes']) for x in topologies}=={(1,1),(1,3),(3,1),(3,3),(3,5)}
migration={'artifact_id':a['id'],'source_member':'ordinary_history/stdout.jsonl','source_member_sha256':sha(body),'topologies':topologies,'history_verification_count':len(history),'history':history,'temporary_retry_sum':sum(x['temporary_retries'] for x in history)};save('migration-history-safe-summary.json',migration)
cleanup=[{'check':n,**r['result']['cleanup']} for n,r in named.items()]+[{'check':n,**r['result']['cleanup']} for n,r in e2e['cases'].items()];assert len(cleanup)==6 and all(x['verified'] and x['complete'] and not x['remaining_processes'] for x in cleanup)
all_events=load(B/'scripts-safe-events.json');selected=[e for e in all_events if e.get('Action') in ['pass','skip','fail'] and (e.get('Test','') in ['TestChatLifecycleTerminalEvidenceRetention','TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire','TestLocalThresholdPprofInterruptJoinsRequestsAndWritesPartialMetadata'] or e.get('Test','').startswith('TestLocalThresholdPprofInterruptDuringRequestRegistration'))]
assert len(selected)==13 and all(x['Action']=='pass' for x in selected)
limits=['Default go-unit nonverbose summaries do not enumerate individual test-case totals. Local test counts are not substituted for Linux evidence.','Scripts opt-in tests, two non-PR three-node jobs and the optional 2000QPS comparison are skipped, not passes. Standalone EasySDK/Manager workflows were not triggered.','Large correctness/native ZIPs remain metadata-only: API declared digests are not locally recomputed full hashes.','The native Go wrapper suppresses successful inner systemd stdout. Evidence binds exact checkout, enabling env, actual command, successful step and Go result; no independent container zero-inventory receipt is claimed.','Optional cosign tool verification was unavailable; no production signing or stable publication occurred.','Retention parent PASS is retained; nested 79 process receipts are not separately uploaded and no per-case Linux receipt is invented.','PR acceptance does not establish GA, resolve historical cloud failures, or prove long-duration stability.','Width16 presence/cluster integration fixtures are independently validated by another auditor; these natural workflow commands do not directly select those integration packages.']
read_failures=[load(p) for p in sorted((B/'proof-inventory').glob('*.read.json')) if load(p).get('problem')]
value={'bounded_log_GET_failures_preserved':read_failures,'observed_at':summary['observed_at'],'source':{'head':H,'base':BASE,'actual_checkout':M,'tree':TREE,'exact_tree_equal':True,'merge_parents':[BASE,H],'actual_checkout_job_count':14},'runs':[{k:r[k] for k in ['id','name','event','run_attempt','status','conclusion','created_at','updated_at','html_url']} for r in runs],'job_conclusions':counts,'all_first_attempt':True,'primary_main_unchanged':primary_main,'all_artifact_workflow_identities_verified':True,'draft_at_final_read':False,'named_checks':named,'default_summary':{k:v for k,v in default.items() if k!='package_summaries'},'scripts_summary':scripts,'selected_scripts_regression_events':selected,'e2e':e2e,'migration_history_count':len(history),'migration_temporary_retry_sum':migration['temporary_retry_sum'],'performance':perf,'owned_SID_cleanup':cleanup,'review_agent_workflows':review,'small_ZIP_count':len(small),'small_ZIP_bytes':sum(x['size_in_bytes'] for x in small),'small_ZIP_members':sum(x['member_count'] for x in small),'full_small_ZIP_size_SHA_CRC_UTF8_verified':True,'all_raw_ZIPs_and_logs_private':True,'metadata_only_artifacts':large,'limitations':limits,'no_workflow_or_GitHub_mutation_source_execution_cloud_action':True,'all_owned_processes_terminal':True,'conclusion':'Exact reviewed tree passes all applicable first-attempt automatic CI; skips and evidence limits remain explicit. Ready/merge belongs to parent.'}
save('validation.json',value)
perf_line=', '.join(k+' '+format(max(w['scheduled_to_completion_p99_ms'] for w in v['measurement_windows']),'.6f')+' ms' for k,v in perf.items());large_line=', '.join(x['name']+' '+str(x['size_in_bytes'])+'B' for x in large)
correctness=next(x['terminal_summary'] for x in load(B/'safe-job-log-summary.json')['jobs'] if 'terminal_summary' in x)
readme=f'''# PR1033 first automatic CI

Head `{H}` and actual checkout `{M}` share tree `{TREE}`. Checkout parents are exact base `{BASE}` and head; 14 completed jobs independently log that checkout. Base/head rules, workflow contracts and policy are byte-identical and rehashed after collection. No actual AGENTS/FLOW source file is archived.

Four natural runs (candidate39893010352, three-node39893010250, native39893010300, signal39893010410) succeeded on first attempt: 16 successful jobs and 2 explicit non-PR skips. Final read still shows an open ready PR, not merged. The four recorded protected Review Agent workflows remained disabled.

Default passed {default['elapsed_seconds']:.3f}s: {default['package_pass_count']} package PASS and {default['no_test_file_summaries']} no-test-file packages. Scripts passed {scripts['elapsed_seconds']:.3f}s: root PASS {scripts['root_counts'].get('pass',0)}, SKIP {scripts['root_counts'].get('skip',0)}; all-level PASS {scripts['all_test_counts'].get('pass',0)}, SKIP {scripts['all_test_counts'].get('skip',0)}; package PASS {scripts['package_counts'].get('pass',0)}, zero FAIL/unfinished. Original base-policy argv/cwd, 1800s/600s outer limits, 8MiB cap, GOFLAGS=-p=2 (scripts additionally -json), Go9m/-parallel2 were verified. Test names are not filtered for Token/Secret/Authorization. Retention, nine registration-signal cases, original interrupt and direct-lab Start passed; inner 79 case receipts were not separately uploaded.

One fixed E2E binary `{e2e['binary_sha256']}` passed all five migration topologies, {len(history)} history checks ({migration['temporary_retry_sum']} temporary retries), cross-node messaging, and self-chat with 1/3 nodes and one full-cluster restart each. Auth refusal and sender suppression passed. Optional 2000QPS comparison is skipped. All six named-check/build/scenario owned-SID cleanup receipts are verified, complete and have no remaining processes.

All three 500QPS seams each completed three 60s measurement windows: 270000 measured planned/started/completed, zero errors/drops, with 90000 separate warmup requests. P99 maxima: {perf_line}; unchanged limit400ms. Exact counter windows, Go1.25.11, four visible Linux AMD64 CPUs and binary/source identities are retained. Correctness separately reports sent={correctness['sent']}, acknowledged={correctness['acknowledged']}, outcome={correctness['outcome']}, cause={correctness['cause']}; its large artifact was not downloaded.

Native build/install, TEST ONLY signed-repository validation and real-systemd lifecycles passed on Ubuntu24.04, Debian12, Rocky9, Alma9. The wrapper suppresses inner success output: this archive binds actual enabled commands/steps and Go summaries, without inventing independent container-zero proof. Optional GoReleaser cosign verification was unavailable. This is not native-channel publication.

Six small ZIPs total {value['small_ZIP_bytes']}B with {value['small_ZIP_members']} UTF8 members; full API size/SHA and CRC/path/type checks passed. All original ZIPs, raw logs and self-chat identities stay private. Metadata-only bodies: {large_line}. Only typed public evidence and hashes are included.

No dispatch/retry/cancel/enable/comment/ready/merge, product/source execution, Go test, provider or cloud operation occurred. The first mixed-send job-log GET reached its 55s read limit; that private raw attempt and public hash receipt remain, and one later read completed. No CI retry occurred. Dedicated width16 presence/cluster integration is separate; natural workflows do not directly run those tagged package fixtures. Passing this PR does not establish GA or 72-hour stability. All auditor subprocesses are terminal.
'''
(B/'README.md').write_text(readme)
top=['README.md','validation.json','source-authority.json','collect-metadata.py','collect-completed.py','validate-small-proofs.py','summarize-job-logs.py','finalize.py','go-unit-result.json','scripts-integration-result.json','default-summary.json','scripts-summary.json','scripts-safe-events.json','performance-tcp-sendack.json','performance-channel-append.json','performance-mixed-send.json','e2e-summary.json','migration-history-safe-summary.json','safe-job-log-summary.json']
names=set(top)
for folder,pattern in [('polls','*/*.json'),('proof-inventory','*.json'),('log-bindings','*.json')]:names.update(p.relative_to(B).as_posix() for p in (B/folder).glob(pattern) if p.is_file())
sensitive=[]
def scan(v,key=''):
 if isinstance(v,dict):
  for k,x in v.items():scan(x,k)
 elif isinstance(v,list):
  for x in v:scan(x,key)
 elif key in ['uid','sender_uid','receiver_uid','channel_id','payload','from_uid','to_uid'] and isinstance(v,str) and len(v)>=6:sensitive.append(v.encode())
scan(json.loads(z.read('self_chat/report.json')))
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
print(json.dumps({'directory':str(B),'payload_files':len(names),'payload_bytes':archive['payload_bytes'],'manifest_entries':len(entries),'SHA256SUMS':archive['sha256sums_sha256'],'archive_sha256':sha((B/'archive-sha256.json').read_bytes()),'small_ZIP_bytes':value['small_ZIP_bytes'],'check_conclusions':counts,'all_owned_processes_terminal':True}))
