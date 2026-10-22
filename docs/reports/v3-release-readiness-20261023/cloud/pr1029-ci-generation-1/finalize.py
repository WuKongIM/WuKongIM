#!/usr/bin/env python3
import pathlib,json,hashlib,collections,re
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1029-ci-generation-1');P=R/'polls/20261022T223650Z'
HEAD='c4954c6a638080e6a915ecec305a3381f4afd9a0';BASE='d349a7bdd2ca8f6bd4683fbccbf83010d3f9cd3e';MERGE='15289a0a91d1b858db667f7783955d022f965913';TREE='98fead03ba0c727dd733f742790e7737327f3942';sha=lambda b:hashlib.sha256(b).hexdigest()
original_ids={39864525576,39864525733,39864525628,39864525579};aux_ids={39865967329}
head=json.loads((R/'head-commit.json').read_text());merge=json.loads((R/'pr-merge-commit.json').read_text());assert head['sha']==HEAD and head['commit']['tree']['sha']==TREE and merge['sha']==MERGE and merge['commit']['tree']['sha']==TREE and [p['sha'] for p in merge['parents']]==[BASE,HEAD]
checks=json.loads((R/'final-pr-checks.json').read_text());assert checks['headRefOid']==HEAD and checks['baseRefOid']==BASE and checks['isDraft'] is True and len(checks['statusCheckRollup'])==19
assert all(c['status']=='COMPLETED' for c in checks['statusCheckRollup']) and collections.Counter(c['conclusion'] for c in checks['statusCheckRollup'])=={'SUCCESS':17,'SKIPPED':2}
runs=[];jobs=[];artifacts=[]
for f in sorted(P.glob('run-*.json')):
 run=json.loads(f.read_text());assert run['head_sha']==HEAD and run['run_attempt']==1 and run['event']=='pull_request' and run['status']=='completed' and run['conclusion']=='success'
 item={k:run[k] for k in ['id','name','path','head_sha','run_attempt','event','status','conclusion','created_at','updated_at','html_url']};item['generation_role']='original_PR_open' if run['id'] in original_ids else 'additional_signal_after_body_edit';runs.append(item)
for f in sorted(P.glob('jobs-*.json')):
 x=json.loads(f.read_text());assert x['total_count']==len(x['jobs']);jobs.extend(x['jobs'])
for f in sorted(P.glob('artifacts-*.json')):
 x=json.loads(f.read_text());assert x['total_count']==len(x['artifacts']);artifacts.extend(x['artifacts'])
assert {x['id'] for x in runs}==original_ids|aux_ids and len(jobs)==19 and len(artifacts)==8 and collections.Counter(j['conclusion'] for j in jobs)=={'success':17,'skipped':2}
original_jobs=[j for j in jobs if j['run_id'] in original_ids];assert len(original_jobs)==18 and collections.Counter(j['conclusion'] for j in original_jobs)=={'success':16,'skipped':2}
collection=json.loads((R/'collected-20261022T223650Z.json').read_text());assert len(collection['logs'])==17
for l in collection['logs']:
 p=R/'logs'/('job-'+str(l['id'])+'.log');b=p.read_bytes();assert len(b)==l['bytes'] and sha(b)==l['sha256']
 if l['name'] in ['Emit credential-free wake-up','PR three-node regression']:assert l['actual_checkout_candidates']==[]
 else:assert l['actual_checkout_candidates']==[MERGE]
source=json.loads((R/'source-authority.json').read_text());assert source['files'][HEAD]==source['files'][BASE]
policy_body=(R/'frozen-source'/BASE/'.github/review-agent/policy.json').read_bytes();policy=json.loads(policy_body);named={}
for prefix,name,env in [('v3-candidate-default-','go-unit',{'GOWORK':'off','GOFLAGS':'-p=2'}),('v3-candidate-scripts-','scripts-integration',{'GOWORK':'off','GOFLAGS':'-p=2 -json'})]:
 aid=str(next(a['id'] for a in artifacts if a['name'].startswith(prefix)));p=R/'verified-artifacts'/aid;ident=json.loads((p/'identity.json').read_text());command=json.loads((p/'command.json').read_text());result=json.loads((p/'result.json').read_text());frozen=policy['trusted_checks'][name]
 assert ident['source_sha']==MERGE and ident['policy_sha']==BASE and ident['policy_sha256']==sha(policy_body) and ident['run_attempt']=='1'
 assert {k:command[k] for k in frozen}==frozen and command['named_check']==name and command['environment']==env
 assert result['passed'] is True and result['status']=='passed' and result['exit_code']==0 and result['wrapper_exit_code']==0 and result['output_truncated'] is False and result['elapsed_seconds']<frozen['timeout_seconds'] and result['output_bytes']<frozen['max_output_bytes']
 assert result['cleanup']['verified'] is True and result['cleanup']['complete'] is True and result['cleanup']['remaining_processes']==[]
 named[name]={'artifact_id':int(aid),'identity':ident,'command':command,'result':result}
e2e=json.loads((R/'e2e-summary.json').read_text());assert e2e['identity']['source_sha']==MERGE
proof_aid=str(next(a['id'] for a in artifacts if a['name'].startswith('v3-candidate-e2e-')));info=(R/'verified-artifacts'/proof_aid/'binary-build-info.txt').read_text();assert 'go1.25.11' in info and 'vcs.revision='+MERGE in info and 'vcs.modified=false' in info
native=[]
for j in jobs:
 if j['run_id']==39864525579:
  assert j['conclusion']=='success';native.append({'id':j['id'],'name':j['name'],'conclusion':j['conclusion'],'successful_steps':[s['name'] for s in j['steps'] if s['conclusion']=='success'],'skipped_steps':[s['name'] for s in j['steps'] if s['conclusion']=='skipped']})
assert len(native)==5
native_log=(R/'logs/job-120123267437.log').read_text();assert 'cosign not found in PATH, skipping signature verification' in native_log
states=json.loads((R/'review-workflow-states.json').read_text());assert {x['state'] for x in states.values()}=={'disabled_manually'}
perf=json.loads((R/'performance-validation.json').read_text());assert len(perf)==3
v={'source_binding':{'reviewed_head':HEAD,'base':BASE,'actual_ci_checkout':MERGE,'tree':TREE,'exact_tree_equal':True,'merge_parents':[BASE,HEAD],'frozen_policy_sha256':sha(policy_body),'frozen_rules_workflows_policy_equal_at_head_and_base':True},'runs':runs,'original_open_event_check_counts':{'success':16,'event_skipped':2},'final_PR_check_counts_including_additional_signal':{'success':17,'event_skipped':2},'all_first_attempt':True,'additional_signal_is_not_a_test_rerun':True,'all_checkout_jobs_bound':True,'checkout_jobs':14,'no_checkout_jobs':{'Issue_Signal':2,'three_node_aggregate':1},'draft_unchanged_by_auditor':True,'named_checks':named,'default_suite_summary':json.loads((R/'default-suite-log-summary.json').read_text()),'scripts_summary':json.loads((R/'scripts-events-summary.json').read_text()),'performance':perf,'e2e':{'binary_sha256':e2e['binary_sha256'],'embedded_compiler':'go1.25.11','embedded_vcs_revision':MERGE,'embedded_vcs_modified':False,'commands_passed':4,'cleanup_receipts_verified_complete_zero_remaining':4,'test_pass_event_counts':{k:len(x['test_pass_events']) for k,x in e2e['cases'].items()},'skipped_opt_in_2000qps_comparison':True,'raw_self_chat_report_and_containing_zip_excluded_from_public_allowlist':True},'native_preview_jobs':native,'native_limitations':['The77,875,382-byte preview package ZIP was not downloaded; API metadata and completed jobs/checksum steps were retained, but its byte size/SHA/member CRCs were not independently checked locally.','No production signing or APT/RPM publication occurred.','GoReleaser download checksum passed; optional cosign verification of the GoReleaser tool itself was skipped because cosign is unavailable.','Successful CI cleanup contracts do not supply a separate independent host-inventory receipt beyond their explicit test/command receipts.'],'manager':{'triggered':False,'coverage_not_claimed':True},'artifacts':collection['artifacts'],'large_artifact_limits':['229,478,934-byte three-node correctness ZIP not downloaded; API metadata and full completed job log retained.','77,875,382-byte native package ZIP not downloaded; API metadata and full completed jobs retained.'],'limits':['The new presence concurrency tests use the integration tag; this ordinary Linux default suite does not supply a dedicated per-case Linux receipt for those new tests. Their separately retained local normal/race evidence is not relabeled Linux.','The full-distribution exact-source native 500 SEND/s process diagnostic failed with sampled expiry and invalid finalization. CI success does not supersede that first failure, establish loss repair, cloud root cause,72-hour stability,2,000-QPS capacity, production publication or GA readiness.'],'no_dispatch_rerun_cancel_merge_cloud_local_build_or_test':True,'conclusion':'All original first-attempt PR CI gates passed on the exact reviewed tree; one additional credential-free signal followed a body edit. The Draft stays unchanged. Passing bounded regression does not repair or invalidate the separately retained failed full-distribution local experiment.'}
(R/'validation.json').write_text(json.dumps(v,indent=2)+'\n')
public=sorted(p.relative_to(R).as_posix() for p in R.rglob('*') if p.is_file() and not any(part.startswith('PRIVATE_') for part in p.relative_to(R).parts) and p.name not in ['SHA256SUMS','PUBLIC_ALLOWLIST.txt'])
assert not any(x.endswith('self_chat/report.json') or x=='verified-downloads/'+proof_aid+'.zip' for x in public)
(R/'PUBLIC_ALLOWLIST.txt').write_text('\n'.join(public)+'\n');manifest=sorted(public+['PUBLIC_ALLOWLIST.txt']);(R/'SHA256SUMS').write_text(''.join(sha((R/p).read_bytes())+'  '+p+'\n' for p in manifest))
for line in (R/'SHA256SUMS').read_text().splitlines():h,p=line.split('  ',1);assert sha((R/p).read_bytes())==h
print(json.dumps({'public_payload_files':len(public),'public_payload_bytes':sum((R/p).stat().st_size for p in public),'manifest_entries':len(manifest),'manifest_sha256':sha((R/'SHA256SUMS').read_bytes()),'allowlist_sha256':sha((R/'PUBLIC_ALLOWLIST.txt').read_bytes()),'validation_sha256':sha((R/'validation.json').read_bytes()),'actual_checkout':MERGE,'tree':TREE,'checks':v['final_PR_check_counts_including_additional_signal'],'downloaded_small_zip_count':sum(a['state'].startswith('verified') for a in collection['artifacts']),'downloaded_small_zip_bytes':sum(a.get('zip_bytes',0) for a in collection['artifacts'])}))
