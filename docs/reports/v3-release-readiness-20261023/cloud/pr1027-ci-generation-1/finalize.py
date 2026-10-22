#!/usr/bin/env python3
import pathlib,json,hashlib,re,datetime,collections
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1027-ci-generation-1');P=R/'polls/20261022T195846Z'
HEAD='a93837d3fb55369acfe2d131aff52914f55a867f';BASE='25a4765be7bed6696e1c9748142bdd61543b508c';MERGE='d3fdd6a4c3eb94020776385efb32fd92b12700a0';TREE='0f24d5dfbdb8de60789fbf8760d5c3a1b3c33344';sha=lambda b:hashlib.sha256(b).hexdigest()
head=json.loads((R/'head-commit.json').read_text());merge=json.loads((R/'pr-merge-commit.json').read_text());assert head['sha']==HEAD and head['commit']['tree']['sha']==TREE and merge['sha']==MERGE and merge['commit']['tree']['sha']==TREE and [p['sha'] for p in merge['parents']]==[BASE,HEAD]
checks=json.loads((R/'final-pr-checks.json').read_text());assert checks['headRefOid']==HEAD and checks['baseRefOid']==BASE and checks['mergeStateStatus']=='CLEAN' and len(checks['statusCheckRollup'])==19
assert collections.Counter(c['conclusion'] for c in checks['statusCheckRollup'])=={'SUCCESS':17,'SKIPPED':2}
runs=[];jobs=[];artifacts=[]
for f in sorted(P.glob('run-*.json')):
 run=json.loads(f.read_text());assert run['head_sha']==HEAD and run['run_attempt']==1 and run['event']=='pull_request' and run['status']=='completed' and run['conclusion']=='success'
 runs.append({k:run[k] for k in ['id','name','path','head_sha','run_attempt','event','status','conclusion','created_at','updated_at','html_url']})
for f in sorted(P.glob('jobs-*.json')):
 obj=json.loads(f.read_text());assert obj['total_count']==len(obj['jobs']);jobs.extend(obj['jobs'])
for f in sorted(P.glob('artifacts-*.json')):
 obj=json.loads(f.read_text());assert obj['total_count']==len(obj['artifacts']);artifacts.extend(obj['artifacts'])
assert len(runs)==5 and len(jobs)==19 and len(artifacts)==8
assert collections.Counter(j['conclusion'] for j in jobs)=={'success':17,'skipped':2}
collection=json.loads((R/'collected-20261022T195846Z.json').read_text());assert len(collection['logs'])==17
for l in collection['logs']:
 p=R/'logs'/('job-'+str(l['id'])+'.log');b=p.read_bytes();assert len(b)==l['bytes'] and sha(b)==l['sha256']
 if l['name'] in ['Emit credential-free wake-up','PR three-node regression']:assert l['actual_checkout_candidates']==[]
 else:assert l['actual_checkout_candidates']==[MERGE]
source=json.loads((R/'source-authority.json').read_text());assert source['files'][HEAD]==source['files'][BASE]
policy_body=(R/'frozen-source'/BASE/'.github/review-agent/policy.json').read_bytes();policy=json.loads(policy_body);named={}
for aid,name,env in [('12454232313','go-unit',{'GOWORK':'off','GOFLAGS':'-p=2'}),('12453848065','scripts-integration',{'GOWORK':'off','GOFLAGS':'-p=2 -json'})]:
 p=R/'verified-artifacts'/aid;ident=json.loads((p/'identity.json').read_text());command=json.loads((p/'command.json').read_text());result=json.loads((p/'result.json').read_text());frozen=policy['trusted_checks'][name]
 assert ident['source_sha']==MERGE and ident['policy_sha']==BASE and ident['policy_sha256']==sha(policy_body) and ident['run_attempt']=='1'
 assert {k:command[k] for k in frozen}==frozen and command['named_check']==name and command['environment']==env
 assert result['passed'] is True and result['status']=='passed' and result['exit_code']==0 and result['wrapper_exit_code']==0 and result['output_truncated'] is False and result['elapsed_seconds']<frozen['timeout_seconds'] and result['output_bytes']<frozen['max_output_bytes']
 assert result['cleanup']['verified'] is True and result['cleanup']['complete'] is True and result['cleanup']['remaining_processes']==[]
 named[name]={'identity':ident,'command':command,'result':result}
e2e=json.loads((R/'e2e-summary.json').read_text());assert e2e['identity']['source_sha']==MERGE
native=[]
for j in jobs:
 if j['run_id']==39842718259:
  assert j['conclusion']=='success'
  native.append({'id':j['id'],'name':j['name'],'conclusion':j['conclusion'],'successful_steps':[s['name'] for s in j['steps'] if s['conclusion']=='success'],'skipped_steps':[s['name'] for s in j['steps'] if s['conclusion']=='skipped']})
assert len(native)==5
native_log=(R/'logs/job-120047790951.log').read_text();assert 'cosign not found in PATH, skipping signature verification' in native_log
manager_job=next(j for j in jobs if j['run_id']==39842718360);manager_log=(R/'logs'/('job-'+str(manager_job['id'])+'.log')).read_text();assert '--- PASS: TestManagerProductionBundleInChromium' in manager_log and '--- PASS: TestRunBrowserCommandReapsTermIgnoringDescendantAfterTimeout' in manager_log
v={'source_binding':{'reviewed_head':HEAD,'base':BASE,'actual_ci_checkout':MERGE,'tree':TREE,'exact_tree_equal':True,'merge_parents':[BASE,HEAD],'frozen_policy_sha256':sha(policy_body),'frozen_rules_workflows_policy_equal_at_head_and_base':True},'runs':runs,'check_counts':{'success':17,'event_skipped':2},'all_first_attempt':True,'all_checkout_jobs_bound':True,'checkout_jobs':15,'no_checkout_jobs':['Issue Signal','three-node aggregate'],'named_checks':named,'e2e':{'binary_sha256':e2e['binary_sha256'],'commands_passed':4,'cleanup_receipts_verified_complete_zero_remaining':4,'test_pass_event_counts':{k:len(x['test_pass_events']) for k,x in e2e['cases'].items()},'skipped_opt_in_2000qps_comparison':True,'raw_self_chat_report_and_container_zip_excluded_from_public_allowlist':True},'native_preview_jobs':native,'native_limitations':['The 77,863,393-byte preview package ZIP was not downloaded; its byte size, API SHA256 and member CRCs were not independently verified locally.','No production signing or APT/RPM publication occurred.','GoReleaser download checksum passed; optional cosign verification of the GoReleaser tool itself was skipped because cosign is unavailable.','Successful test/workflow status does not supply an independent host inventory receipt beyond the CI test cleanup contract.'],'manager':{'desktop_and_mobile_chromium_case_pass':True,'timeout_descendant_cleanup_test_pass':True,'success_path_has_no_uploaded_browser_artifact':True},'artifacts':collection['artifacts'],'large_artifact_limits':['229,645,552-byte three-node correctness ZIP not downloaded; only API metadata and full completed job log retained.','77,863,393-byte native package ZIP not downloaded; only API metadata and full completed jobs retained.'],'no_dispatch_rerun_cancel_merge_cloud_or_local_test':True,'offline_parser_failure_boundary_retained':'e2e-validation-first-assertion-boundary.json','conclusion':'All current first-attempt PR CI gates passed on the exact reviewed tree. This proves bounded PR regression only; the cloud failure, 72-hour soak, root cause and signed production publication remain unqualified.'}
(R/'validation.json').write_text(json.dumps(v,indent=2)+'\n')
# Finite public allowlist excludes all private originals and the raw self-chat report.
public=sorted(p.relative_to(R).as_posix() for p in R.rglob('*') if p.is_file() and not any(part.startswith('PRIVATE_') for part in p.relative_to(R).parts) and p.name not in ['SHA256SUMS','PUBLIC_ALLOWLIST.txt'])
assert not any(x.endswith('self_chat/report.json') or x=='verified-downloads/12454502104.zip' for x in public)
(R/'PUBLIC_ALLOWLIST.txt').write_text('\n'.join(public)+'\n')
manifest=public+['PUBLIC_ALLOWLIST.txt'];(R/'SHA256SUMS').write_text(''.join(sha((R/p).read_bytes())+'  '+p+'\n' for p in sorted(manifest)))
for p in manifest:assert (R/p).is_file()
print(json.dumps({'public_payload_files':len(public),'public_payload_bytes':sum((R/p).stat().st_size for p in public),'manifest_entries':len(manifest),'manifest_sha256':sha((R/'SHA256SUMS').read_bytes()),'allowlist_sha256':sha((R/'PUBLIC_ALLOWLIST.txt').read_bytes()),'actual_checkout':MERGE,'tree':TREE,'checks':v['check_counts'],'downloaded_small_zip_count':sum(a['state'].startswith('verified') for a in collection['artifacts']),'downloaded_small_zip_bytes':sum(a.get('zip_bytes',0) for a in collection['artifacts'])}))
