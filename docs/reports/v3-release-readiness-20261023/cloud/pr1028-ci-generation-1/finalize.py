#!/usr/bin/env python3
import pathlib,json,hashlib,collections,re
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1028-ci-generation-1');P=R/'polls/20261022T211806Z'
HEAD='3d243bccc8caed21a8719071ad7c1883156713c2';BASE='2ab3e1a8bd95670603bc38314036c797a5124682';MERGE='11e66c4e3f45a01750741e225f2feb246e60e8c0';TREE='763f8432135e6b94df28451a43a4a635c7aba2a0';sha=lambda b:hashlib.sha256(b).hexdigest()
head=json.loads((R/'head-commit.json').read_text());merge=json.loads((R/'pr-merge-commit.json').read_text());assert head['sha']==HEAD and head['commit']['tree']['sha']==TREE and merge['sha']==MERGE and merge['commit']['tree']['sha']==TREE and [p['sha'] for p in merge['parents']]==[BASE,HEAD]
checks=json.loads((R/'final-pr-checks.json').read_text());assert checks['headRefOid']==HEAD and checks['baseRefOid']==BASE and checks['mergeStateStatus']=='CLEAN' and len(checks['statusCheckRollup'])==18
assert all(c['status']=='COMPLETED' for c in checks['statusCheckRollup']) and collections.Counter(c['conclusion'] for c in checks['statusCheckRollup'])=={'SUCCESS':16,'SKIPPED':2}
runs=[];jobs=[];artifacts=[]
for f in sorted(P.glob('run-*.json')):
 run=json.loads(f.read_text());assert run['head_sha']==HEAD and run['run_attempt']==1 and run['event']=='pull_request' and run['status']=='completed' and run['conclusion']=='success'
 runs.append({k:run[k] for k in ['id','name','path','head_sha','run_attempt','event','status','conclusion','created_at','updated_at','html_url']})
for f in sorted(P.glob('jobs-*.json')):
 x=json.loads(f.read_text());assert x['total_count']==len(x['jobs']);jobs.extend(x['jobs'])
for f in sorted(P.glob('artifacts-*.json')):
 x=json.loads(f.read_text());assert x['total_count']==len(x['artifacts']);artifacts.extend(x['artifacts'])
assert len(runs)==4 and len(jobs)==18 and len(artifacts)==8 and collections.Counter(j['conclusion'] for j in jobs)=={'success':16,'skipped':2}
collection=json.loads((R/'collected-20261022T211806Z.json').read_text());assert len(collection['logs'])==16
for l in collection['logs']:
 p=R/'logs'/('job-'+str(l['id'])+'.log');b=p.read_bytes();assert len(b)==l['bytes'] and sha(b)==l['sha256']
 if l['name'] in ['Emit credential-free wake-up','PR three-node regression']:assert l['actual_checkout_candidates']==[]
 else:assert l['actual_checkout_candidates']==[MERGE]
source=json.loads((R/'source-authority.json').read_text());assert source['files'][HEAD]==source['files'][BASE]
policy_body=(R/'frozen-source'/BASE/'.github/review-agent/policy.json').read_bytes();policy=json.loads(policy_body);named={}
for aid,name,env in [('12458697607','go-unit',{'GOWORK':'off','GOFLAGS':'-p=2'}),('12459108207','scripts-integration',{'GOWORK':'off','GOFLAGS':'-p=2 -json'})]:
 p=R/'verified-artifacts'/aid;ident=json.loads((p/'identity.json').read_text());command=json.loads((p/'command.json').read_text());result=json.loads((p/'result.json').read_text());frozen=policy['trusted_checks'][name]
 assert ident['source_sha']==MERGE and ident['policy_sha']==BASE and ident['policy_sha256']==sha(policy_body) and ident['run_attempt']=='1'
 assert {k:command[k] for k in frozen}==frozen and command['named_check']==name and command['environment']==env
 assert result['passed'] is True and result['status']=='passed' and result['exit_code']==0 and result['wrapper_exit_code']==0 and result['output_truncated'] is False and result['elapsed_seconds']<frozen['timeout_seconds'] and result['output_bytes']<frozen['max_output_bytes']
 assert result['cleanup']['verified'] is True and result['cleanup']['complete'] is True and result['cleanup']['remaining_processes']==[]
 named[name]={'identity':ident,'command':command,'result':result}
e2e=json.loads((R/'e2e-summary.json').read_text());assert e2e['identity']['source_sha']==MERGE
info=(R/'verified-artifacts/12459068339/binary-build-info.txt').read_text();assert 'go1.25.11' in info and 'vcs.revision='+MERGE in info and 'vcs.modified=false' in info
native=[]
for j in jobs:
 if j['run_id']==39854388530:
  assert j['conclusion']=='success';native.append({'id':j['id'],'name':j['name'],'conclusion':j['conclusion'],'successful_steps':[s['name'] for s in j['steps'] if s['conclusion']=='success'],'skipped_steps':[s['name'] for s in j['steps'] if s['conclusion']=='skipped']})
assert len(native)==5
native_log=(R/'logs/job-120088657304.log').read_text();assert 'cosign not found in PATH, skipping signature verification' in native_log
before=json.loads((R/'premerge-before/summary.json').read_text());assert before['review_workflows']=={'review-controller':'disabled_manually','review-runner':'disabled_manually'}
transport=json.loads((R/'metadata-first-transport-error.json').read_text());assert transport['not_a_ci_failure'] is True
v={'source_binding':{'reviewed_head':HEAD,'base':BASE,'actual_ci_checkout':MERGE,'tree':TREE,'exact_tree_equal':True,'merge_parents':[BASE,HEAD],'frozen_policy_sha256':sha(policy_body),'frozen_rules_workflows_policy_equal_at_head_and_base':True},'runs':runs,'check_counts':{'success':16,'event_skipped':2},'all_first_attempt':True,'all_checkout_jobs_bound':True,'checkout_jobs':14,'no_checkout_jobs':['Issue Signal','three-node aggregate'],'named_checks':named,'e2e':{'binary_sha256':e2e['binary_sha256'],'embedded_compiler':'go1.25.11','embedded_vcs_revision':MERGE,'embedded_vcs_modified':False,'commands_passed':4,'cleanup_receipts_verified_complete_zero_remaining':4,'test_pass_event_counts':{k:len(x['test_pass_events']) for k,x in e2e['cases'].items()},'skipped_opt_in_2000qps_comparison':True,'raw_self_chat_report_and_containing_zip_excluded_from_public_allowlist':True},'native_preview_jobs':native,'native_limitations':['The77,858,326-byte preview package ZIP was not downloaded; API metadata and completed jobs/checksum steps were retained, but its byte size/SHA/member CRCs were not independently checked locally.','No production signing or APT/RPM publication occurred.','GoReleaser download checksum passed; optional cosign verification of the GoReleaser tool itself was skipped because cosign is unavailable.','Successful CI cleanup contracts do not supply a separate independent host-inventory receipt beyond their explicit test/command receipts.'],'manager':{'triggered':False,'coverage_not_claimed':True},'artifacts':collection['artifacts'],'large_artifact_limits':['229,504,823-byte three-node correctness ZIP not downloaded; API metadata and full completed job log retained.','77,858,326-byte native package ZIP not downloaded; API metadata and full completed jobs retained.'],'metadata_read_boundary':{'one_EOF_partial_snapshot_retained':transport['snapshot'],'final_snapshot_complete':P.name,'ci_failure_or_rerun_caused':False},'premerge_readonly_before_snapshot':'premerge-before','no_dispatch_rerun_cancel_merge_cloud_local_build_or_test':True,'conclusion':'All current first-attempt PR CI gates passed on the exact reviewed tree. Partial earlier metadata read is retained, not a failed CI. This proves bounded PR regression only, not original cloud root cause,72-hour soak or production publication.'}
(R/'validation.json').write_text(json.dumps(v,indent=2)+'\n')
public=sorted(p.relative_to(R).as_posix() for p in R.rglob('*') if p.is_file() and not any(part.startswith('PRIVATE_') for part in p.relative_to(R).parts) and p.name not in ['SHA256SUMS','PUBLIC_ALLOWLIST.txt'])
assert not any(x.endswith('self_chat/report.json') or x=='verified-downloads/12459068339.zip' for x in public)
(R/'PUBLIC_ALLOWLIST.txt').write_text('\n'.join(public)+'\n');manifest=sorted(public+['PUBLIC_ALLOWLIST.txt']);(R/'SHA256SUMS').write_text(''.join(sha((R/p).read_bytes())+'  '+p+'\n' for p in manifest))
for line in (R/'SHA256SUMS').read_text().splitlines():h,p=line.split('  ',1);assert sha((R/p).read_bytes())==h
print(json.dumps({'public_payload_files':len(public),'public_payload_bytes':sum((R/p).stat().st_size for p in public),'manifest_entries':len(manifest),'manifest_sha256':sha((R/'SHA256SUMS').read_bytes()),'allowlist_sha256':sha((R/'PUBLIC_ALLOWLIST.txt').read_bytes()),'validation_sha256':sha((R/'validation.json').read_bytes()),'actual_checkout':MERGE,'tree':TREE,'checks':v['check_counts'],'downloaded_small_zip_count':sum(a['state'].startswith('verified') for a in collection['artifacts']),'downloaded_small_zip_bytes':sum(a.get('zip_bytes',0) for a in collection['artifacts'])}))
