#!/usr/bin/env python3
import pathlib,json,hashlib,collections,re,zipfile
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1030-ci-generation-1');P=sorted((R/'polls').iterdir())[-1]
HEAD='56c287493a5e9fc3df65ea0631b99612672cd678';BASE='27fa6d63cf3b2b6c3a9a28575f917d60cc054ded';MERGE='a514c73b4c950b9ead150c28ff08ef89123f5238';TREE='582e5bc024147b32aa68074d04c041391af89b96'
original_ids={39869765431,39869765472,39869765510,39869765462};sha=lambda b:hashlib.sha256(b).hexdigest()
head=json.loads((R/'head-commit.json').read_text());merge=json.loads((R/'pr-merge-commit.json').read_text())
assert head['sha']==HEAD and head['commit']['tree']['sha']==TREE and merge['sha']==MERGE and merge['commit']['tree']['sha']==TREE and [p['sha'] for p in merge['parents']]==[BASE,HEAD]
checks=json.loads((R/'final-pr-checks.json').read_text());assert checks['headRefOid']==HEAD and checks['baseRefOid']==BASE and checks['isDraft'] is True
assert len(checks['statusCheckRollup'])==18 and all(c['status']=='COMPLETED' for c in checks['statusCheckRollup']) and collections.Counter(c['conclusion'] for c in checks['statusCheckRollup'])=={'SUCCESS':16,'SKIPPED':2}
runs=[];jobs=[];artifacts=[]
for f in sorted(P.glob('run-*.json')):
 run=json.loads(f.read_text());assert run['head_sha']==HEAD and run['run_attempt']==1 and run['event']=='pull_request' and run['status']=='completed' and run['conclusion']=='success'
 runs.append({k:run[k] for k in ['id','name','path','head_sha','run_attempt','event','status','conclusion','created_at','updated_at','html_url']})
assert {r['id'] for r in runs}==original_ids
for f in P.glob('jobs-*.json'):
 x=json.loads(f.read_text());assert x['total_count']==len(x['jobs']);jobs.extend(x['jobs'])
for f in P.glob('artifacts-*.json'):
 x=json.loads(f.read_text());assert x['total_count']==len(x['artifacts']);artifacts.extend(x['artifacts'])
assert len(jobs)==18 and len(artifacts)==8 and collections.Counter(j['conclusion'] for j in jobs)=={'success':16,'skipped':2}
collection=json.loads((R/('collected-'+P.name+'.json')).read_text());assert len(collection['logs'])==16
for item in collection['logs']:
 b=(R/'logs'/('job-'+str(item['id'])+'.log')).read_bytes();assert len(b)==item['bytes'] and sha(b)==item['sha256']
 if item['name'] in ['Emit credential-free wake-up','PR three-node regression']:assert item['actual_checkout_candidates']==[]
 else:assert item['actual_checkout_candidates']==[MERGE]
source=json.loads((R/'source-authority.json').read_text())
same=[n for n in source['files'][HEAD] if not n.endswith('/FLOW.md')]
assert all(source['files'][HEAD][n]==source['files'][BASE][n] for n in same)
named=json.loads((R/'named-check-validation.json').read_text());assert set(named)=={'go-unit','scripts-integration'}
default=json.loads((R/'default-suite-log-summary.json').read_text());scripts=json.loads((R/'scripts-events-summary.json').read_text());assert not scripts['failed_tests']
e2e=json.loads((R/'e2e-summary.json').read_text());assert e2e['identity']['source_sha']==MERGE
aid=str(next(a['id'] for a in artifacts if a['name'].startswith('v3-candidate-e2e-')))
info=(R/'verified-artifacts'/aid/'binary-build-info.txt').read_text();assert 'go1.25.11' in info and 'vcs.revision='+MERGE in info and 'vcs.modified=false' in info
performance=json.loads((R/'performance-validation.json').read_text());assert set(performance)=={'channel-append','mixed-send','tcp-sendack'}
native=[{'id':j['id'],'name':j['name'],'conclusion':j['conclusion'],'successful_steps':[s['name'] for s in j['steps'] if s['conclusion']=='success'],'skipped_steps':[s['name'] for s in j['steps'] if s['conclusion']=='skipped']} for j in jobs if j['run_id']==39869765431];assert len(native)==5 and all(j['conclusion']=='success' for j in native)
native_build=next(j for j in native if j['name']=='Build and install deb/rpm preview')
native_log=(R/'logs'/('job-'+str(native_build['id'])+'.log')).read_text()
assert 'cosign not found in PATH, skipping signature verification' in native_log
states=json.loads((R/'review-workflow-states.json').read_text());assert {x['state'] for x in states.values()}=={'disabled_manually'}
private=zipfile.ZipFile(next((R/'PRIVATE_original-downloads').glob('*.zip')));report=json.loads(private.read('self_chat/report.json'));sensitive=[]
def scan(x,key=''):
 if isinstance(x,dict):
  for k,v in x.items():scan(v,k)
 elif isinstance(x,list):
  for v in x:scan(v,key)
 elif key in {'uid','sender_uid','receiver_uid','channel_id','payload','from_uid','to_uid'} and isinstance(x,str) and len(x)>=6:sensitive.append(x.encode())
scan(report)
validation={'source_binding':{'head':HEAD,'base':BASE,'actual_ci_checkout':MERGE,'tree':TREE,'exact_tree_equal':True,'merge_parents':[BASE,HEAD],'frozen_rules_workflows_policy_equal':True,'applicable_FLOWs_separately_frozen':source['applicable_flow']},'runs':runs,'first_generation_checks':{'success':16,'event_skipped':2},'all_first_attempt':True,'checkout_jobs':14,'no_checkout_jobs':['credential-free signal','three-node aggregate'],'draft_unchanged_by_auditor':True,'named_checks':named,'default_suite':default,'scripts_integration':scripts,'performance':performance,'e2e':{'binary_sha256':e2e['binary_sha256'],'embedded_compiler':'go1.25.11','embedded_vcs_revision':MERGE,'embedded_vcs_modified':False,'commands_passed':4,'cleanup_receipts_verified_complete_zero_remaining':4,'test_pass_event_counts':{k:len(v['test_pass_events']) for k,v in e2e['cases'].items()},'optional_2000QPS_comparison_skipped':True,'raw_report_and_containing_zip_private':True},'native_preview_jobs':native,'artifacts':collection['artifacts'],'review_agent_workflows':states,'manager_browser_workflow_triggered':False,'limits':['Default suite is nonverbose: package pass summaries do not provide individual test case receipts.','The228677232-byte correctness ZIP and77886009-byte native package ZIP were not downloaded; only API metadata, full completed job logs and their reported checks retained.','The temporary signing-repository test used TEST ONLY keys; no production signing or APT/RPM publication occurred.','GoReleaser download checksum passed; optional cosign verification skipped because cosign was unavailable.','Missing signal beyond successful CI cleanup contracts does not become an independent host inventory receipt.','New diagnostics do not repair the previously frozen full-distribution exact-c495 expiry. This CI does not prove full-distribution loss repair, root cause,72-hour stability,2000QPS capacity or GA readiness.'],'no_dispatch_rerun_cancel_merge_cloud_local_build_or_test':True,'conclusion':'All original first-attempt PR CI checks passed on the exact reviewed tree. Diagnostic-only candidate; old product failure remains. Auditor did not mark ready or merge.'}
(R/'validation.json').write_text(json.dumps(validation,indent=2)+'\n')
(R/'README.md').write_text('''# PR1030 first automatic CI generation

This finite proof covers the original four automatic pull_request workflow runs, each attempt1. All18 checks completed:16 success and2 explicit non-PR event skips. There was no dispatch, replay, rerun, cancellation, workflow enable, ready/merge action, cloud operation or local build/test by this auditor.

Reviewed head56c287493a5e9fc3df65ea0631b99612672cd678 and test merge a514c73b4c950b9ead150c28ff08ef89123f5238 have identical tree582e5bc024147b32aa68074d04c041391af89b96. Merge parents are base27fa6d63cf3b2b6c3a9a28575f917d60cc054ded and reviewed head56. Fourteen checkout job logs bind the actual test merge; the credential-free signal and aggregate have no checkout. Root rules, workflow contract and policy are unchanged at head/base; both applicable valid FLOWs are separately frozen, including their candidate edits.

Candidate default go-unit, independent scripts-integration and serial process E2E passed their exact frozen policy commands, bounds and cleanup contracts. Migration covers all five topologies, including1-to3, followed by cross-node delivery and whole-cluster restart self-chat on1/3nodes with one exact binary. The optional2000QPS comparison is explicitly skipped and not qualification evidence. See named-check-validation.json, e2e-summary.json and the private-report-free self-chat-safe-summary.json.

Three fresh500QPS seams each retain one60s warmup and three60s measured windows, exact90000 measured completions withzero errors/drops and all windowP99<=400ms. Host, Go, source, binary, filesystem and counter-window identities are retained. These are shared hosted runner observations, not a causal performance comparison.

Native preview passed package/CLI contracts, real single-node cluster SEND, TEST ONLY signed repository closure, non-activating package transactions and real-systemd lifecycle on Ubuntu24.04,Debian12,Rocky9,Alma9. No production key, signed channel or publication was used. Optional cosign verification of downloaded GoReleaser was skipped because cosign was unavailable; its download checksum passed.

Six small proof ZIPs have full API size/SHA256, safe member paths/types, member CRC and content hash validation. The original E2E ZIP and raw identity/payload report remain under PRIVATE_original-downloads and are outside PUBLIC_ALLOWLIST.txt. Only allowlisted small text proofs and a finite typed self-chat summary are exposed. Public members are scanned against private report identity/payload string tokens. Large correctness228677232B and native77886009B ZIPs remain metadata-only: no independent local content digest/member/CRC claim for them. API digests and successful job/checksum steps are separate evidence.

CI success does not invalidate the retained exact-c495 full-distribution failure, repair message loss, establish its root cause, prove72-hour stability or2000QPS capacity, publish APT/RPM or qualify GA. This diagnostic-only PR remains Draft at the audit cut; only root decides readiness/merge after both reviews and runtime evidence assessment. Missing or skipped coverage stays explicit.

Replay validators against already retained files only: validate-small-proofs.py, validate-e2e.py and finalize.py. Metadata/collection helpers are bounded read-only GitHub consumers; do not run them as an automatic test retry. SHA256SUMS covers exactly PUBLIC_ALLOWLIST.txt and its finite file list; private original material is excluded.
''')
public=sorted(p.relative_to(R).as_posix() for p in R.rglob('*') if p.is_file() and not any(part.startswith('PRIVATE_') for part in p.relative_to(R).parts) and p.name not in ['SHA256SUMS','PUBLIC_ALLOWLIST.txt'])
assert not any(n.endswith('self_chat/report.json') or n=='verified-downloads/'+aid+'.zip' for n in public)
for n in public:
 b=(R/n).read_bytes();assert not any(token in b for token in sensitive),('private_identity_token_in_public_member',n)
(R/'PUBLIC_ALLOWLIST.txt').write_text('\n'.join(public)+'\n');manifest=sorted(public+['PUBLIC_ALLOWLIST.txt'])
(R/'SHA256SUMS').write_text(''.join(sha((R/n).read_bytes())+'  '+n+'\n' for n in manifest))
for line in (R/'SHA256SUMS').read_text().splitlines():h,n=line.split('  ',1);assert h==sha((R/n).read_bytes())
print(json.dumps({'public_payload_files':len(public),'public_payload_bytes':sum((R/n).stat().st_size for n in public),'manifest_entries':len(manifest),'manifest_sha256':sha((R/'SHA256SUMS').read_bytes()),'allowlist_sha256':sha((R/'PUBLIC_ALLOWLIST.txt').read_bytes()),'validation_sha256':sha((R/'validation.json').read_bytes()),'checkout':MERGE,'tree':TREE,'first_generation_checks':validation['first_generation_checks'],'verified_small_zip_count':sum(a['state'].startswith('verified') for a in collection['artifacts']),'verified_small_zip_bytes':sum(a.get('zip_bytes',0) for a in collection['artifacts']),'public_identity_token_scan':{'private_tokens':len(sensitive),'matching_members':0}}))
