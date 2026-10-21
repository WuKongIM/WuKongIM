from pathlib import Path
import json,hashlib,gzip,re,zlib,datetime
P=Path(__file__).resolve().parent
read=lambda n:json.loads((P/n).read_text())
e=read('expected.json');H=e['head'];B=e['base'];T=e['tree'];M=read('merge-commit.json')['sha']
pr=read('pr-final.json');assert pr['head']['sha']==H and pr['base']['sha']==B and pr['draft'] and pr['state']=='open'
assert read('merge-commit.json')['tree']['sha']==T==read('head-commit.json')['tree']['sha']
logs={n:(gzip.decompress((P/n).read_bytes()).decode() if n.endswith('.gz') else (P/n).read_text()) for n in ['pages-job.log','legacy-native-job.log.gz','stable-job.log']}
for name,t in logs.items():assert M in t
identity={'head':H,'base':B,'head_tree':T,'synthetic_checkout':M,'synthetic_tree':T,'merge_parents':[x['sha'] for x in read('merge-commit.json')['parents']],'all_three_job_logs_contain_actual_checkout':list(logs),'stable_receipt_control':read('artifacts/native-stable-four-clients-test-only/native-stable-clients.json')['control'],'head_unchanged_after_terminal':True}
(P/'identity-proof-final.json').write_text(json.dumps(identity,indent=2)+'\n')
v=read('receipt-verification.json');assert v['receipt_audit_passed'] and not v['ci_passed']
jobs=read('native-jobs-final.json')['jobs'];d=read('artifacts/native-stable-four-clients-test-only/native-stable-clients.json');f=read('first-failure.json')
baselines=[]
for gen in ['A','B']:
 c=read('artifacts/native-stable-four-clients-test-only/clients-'+gen+'.json')
 for a in c['apt']+c['rpm']:
  assert a['installed_cli']['identity']['build_source']=='release'
  baselines.append({'generation':gen,'distribution':a['distribution'],'identity':a['installed_cli']['identity'],'checks':a['installed_cli']['checks'],'download':a['download'],'installed_cli_verified':a['installed_cli']['verified']})
assert len(baselines)==8
(P/'eight-baseline-identities.json').write_text(json.dumps(baselines,indent=2)+'\n')
summary={'pr':'https://github.com/WuKongIM/packages/pull/75','head':H,'base':B,'tree':T,'attempt':1,'pr_draft':True,'all_runs_terminal':True,'all_observed_checks_passed':False,'pages':{'run':e['pages_run'],'conclusion':'success','python_tests':557,'skipped':0,'publication_control_tests':4,'configure_upload_deploy':'skipped'},'native':{'run':e['native_run'],'conclusion':'failure','actual_suites':v['legacy'],'complete_signed_repository_build_verify_install_passed':True},'stable':{'planned_cases':49,'executed_cases':23,'passed_cases':22,'failed_cases':1,'not_reached':26,'failed_case_number':23,'failed_case':d['phase'],'failure':d['failure'],'new_metadata_override_present':True,'download_process_exit':0,'downloaded_version':'3.0.1','expected_barrier_snapshot':'A/3.0.0','http_serial':24,'command_index_zero_based':161,'baseline_installations_verified':8,'product_source':'fc18221282f289088ec9b4282fda8e0bfc67a3b8','reader_cleanup':f['reader_cleanup'],'cleanup':f['final_cleanup'],'cache_cleanup':d['cache_cleanup'],'fourth_cache_not_created':True,'jobs':[{k:j[k] for k in ('id','name','conclusion','started_at','completed_at')} for j in jobs],'command_bound_seconds':3300,'kill_after_seconds':45,'job_bound_minutes':60},'test_only':True,'published':False,'public_origin_verified':False,'source_provenance_verified':False,'reruns_dispatched':0,'production_mutations':0,'limits':['Generation 1 failure remains retained unchanged.','Both RPM switch acceptances are unverified: Rocky failed, Alma not reached.','No public package publication or public-origin/source-provenance acceptance.','No unique cause attributed; observed second-generation metadata/payload selection contradicts exact-A acceptance.'],'observer_exec_sessions':'all completed before sealing'}
(P/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
(P/'README.md').write_text(f'''# Packages PR #75 — second CI generation

Head `{H}`, base `{B}`, reviewed tree `{T}`. All three executed jobs checked out synthetic merge `{M}`; its complete tree equals the head. Both natural workflows are terminal on attempt 1. No rerun, dispatch, cancellation, source edit, PR status edit, merge or cloud action was performed by this observer.

Pages run {e['pages_run']} passed 557 Linux Python tests with zero skips and four publication-control tests. Configure Pages, artifact upload and deployment were skipped. Native run {e['native_run']} passed the original complete build/sign/verify/install drill but failed the stable-client job. Four small receipts prove 7 metadata, 10 signing, 6 bootstrap, 31 candidate and 8 transport cases; their prescribed scratch/secret/control checks passed.

The stable driver passed 22 cases and failed case 23, `rocky-linux-9-A-to-B-request-barrier`; 26 of 49 cases were not reached. Its actual container command includes the new `--setopt=wukongim-stable.metadata_expire=-1 install --downloadonly`. The native download command (zero-based index 161) exited 0 in 1.782 seconds, verified a signed 3.0.1 RPM, then the host exact-A snapshot guard rejected it: `downloaded rpm package is absent from the indexed snapshot`. Serial 24 contains 17 complete HTTP200 responses: A mirrorlist/A metadata, then B mirrorlist/B signed metadata/B3.0.1 RPM. B payload SHA/size match the B baseline. This is observed behavior, not a unique root-cause attribution. Neither RPM switch proof passed in this run: Rocky failed and Alma was not reached.

Both A/3.0.0 and B/3.0.1 baseline installations passed Ubuntu24.04, Debian13, Rocky9 and Alma9. Eight installed CLI receipts bind the version and TEST ONLY product source `fc18221282f289088ec9b4282fda8e0bfc67a3b8`, including original version/help/bench/query/migrate/invalid-input/read-only-data checks. Those baselines do not make the 49-case suite pass. Reader and final cleanup prove empty owned container/network inventories and no errors. Three created cache directories were removed with no remaining paths; the fourth (Alma) was never created.

Start with `summary.json`, `identity-proof-final.json`, `first-failure.json`, `eight-baseline-identities.json` and `receipt-verification.json`. `verify-receipts.py` checks archived failure and partial-success evidence offline. `receipt_audit_passed=true` means evidence verification passed, not CI. Complete safe job logs are retained; the legacy native log is losslessly compressed. All five small artifacts were downloaded completely and API size/SHA plus every ZIP CRC verified. Only JSON/JSONL members were retained publicly; no product packages or private keys are included. Raw downloads/stderr are in a private sibling directory outside this allowlist.

Generation 1 failure and its diagnosis remain unchanged. These are TEST ONLY results, not package publication, public-origin verification, global CDN atomicity or live source provenance. One initial Pages artifact-inventory GET failed; its receipt remains and a later normal snapshot succeeded. `observer-tool-correction.json` records a local verifier preparation path error and the success-only draft rejecting this failed run, followed by the failure-specific offline audit; no CI retry was performed.
''')
exclude={'ARCHIVE_ALLOWLIST.txt','SHA256SUMS','CRC32SUMS','archive-sha256.json'};files=[]
for p in sorted(P.rglob('*')):
 if not p.is_file() or '__pycache__' in p.parts or p.name in exclude:continue
 assert not p.is_symlink()
 raw=p.read_bytes();b=gzip.decompress(raw) if p.suffix=='.gz' else raw
 assert not re.search(rb'-----BEGIN (?:OPENSSH |RSA |EC |DSA )?PRIVATE KEY|github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,}',b),p
 if p.suffix=='.json':json.loads(b)
 if p.suffix=='.jsonl':
  for line in b.splitlines():json.loads(line)
 files.append({'path':str(p.relative_to(P)),'bytes':len(raw),'sha256':hashlib.sha256(raw).hexdigest(),'crc32':format(zlib.crc32(raw)&0xffffffff,'08x')})
(P/'ARCHIVE_ALLOWLIST.txt').write_text(''.join(x['path']+'\n' for x in files))
(P/'SHA256SUMS').write_text(''.join(x['sha256']+'  '+x['path']+'\n' for x in files))
(P/'CRC32SUMS').write_text(''.join(x['crc32']+'  '+x['path']+'\n' for x in files))
manifest={'schema':'public-evidence-allowlist/v1','frozen_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'head':H,'tree':T,'payload_count':len(files),'payload_bytes':sum(x['bytes'] for x in files),'files':files}
(P/'archive-sha256.json').write_text(json.dumps(manifest,indent=2)+'\n')
for x in files:assert hashlib.sha256((P/x['path']).read_bytes()).hexdigest()==x['sha256']
print(json.dumps({'payload_count':len(files),'payload_bytes':manifest['payload_bytes'],'manifest_sha256':hashlib.sha256((P/'archive-sha256.json').read_bytes()).hexdigest(),'ci_passed':False,'all_terminal':True}))
