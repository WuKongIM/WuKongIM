from pathlib import Path
import json,hashlib,gzip,re,zlib,datetime
P=Path(__file__).resolve().parent
read=lambda n:json.loads((P/n).read_text())
H='eac9d252f13ea201b405825e6303de703af08c16';B='620bdfa1804c732dc1b3b072bb64dbe6f0ae0475';T='ca255a7d0fc27186a68f291ccf7ce70948a976d1';M='0c87528bd58a40413924b51cdc251c183382826c'
pr=read('pr-final.json');assert pr['head']['sha']==H and pr['base']['sha']==B and pr['draft'] and pr['state']=='open'
assert read('merge-commit.json')['tree']['sha']==T==read('head-commit.json')['tree']['sha']
logs={n:(gzip.decompress((P/n).read_bytes()).decode() if n.endswith('.gz') else (P/n).read_text()) for n in ['pages-job.log','legacy-native-job.log.gz','stable-job.log']}
for name,t in logs.items():assert M in t
identity={'head':H,'base':B,'head_tree':T,'synthetic_checkout':M,'synthetic_tree':read('merge-commit.json')['tree']['sha'],'merge_parents':[x['sha'] for x in read('merge-commit.json')['parents']],'all_three_job_logs_contain_actual_checkout':list(logs),'stable_receipt_control':read('artifacts/native-stable-four-clients-test-only/native-stable-clients.json')['control'],'head_unchanged_after_terminal':True}
(P/'identity-proof-final.json').write_text(json.dumps(identity,indent=2)+'\n')
v=read('receipt-verification.json');assert v['receipt_audit_passed'] and v['ci_passed'] is False
jobs=read('native-jobs-final.json')['jobs'];d=read('artifacts/native-stable-four-clients-test-only/native-stable-clients.json');f=read('first-failure.json')
baselines=[]
for gen in ['A','B']:
 c=read('artifacts/native-stable-four-clients-test-only/clients-'+gen+'.json')
 for a in c['apt']+c['rpm']:
  assert a['installed_cli']['identity']['build_source']=='release'
  baselines.append({'generation':gen,'distribution':a['distribution'],'identity':a['installed_cli']['identity'],'checks':a['installed_cli']['checks'],'download':a['download'],'installed_cli_verified':a['installed_cli']['verified']})
(P/'eight-baseline-identities.json').write_text(json.dumps(baselines,indent=2)+'\n')
summary={'pr':'https://github.com/WuKongIM/packages/pull/75','head':H,'base':B,'tree':T,'attempt':1,'pr_draft':True,'all_runs_terminal':True,'all_required_checks_passed':False,'pages':{'run':39631486007,'job':119330563044,'conclusion':'success','python_tests':554,'skipped':0,'publication_control_tests':4,'deploy':'skipped'},'native':{'run':39631486744,'conclusion':'failure','original_job':119330566802,'original_conclusion':'success','actual_suites':v['legacy'],'complete_signed_repository_build_verify_install_passed':True},'stable':{'job':119330566284,'planned_cases':49,'executed_cases':34,'passed_cases':33,'failed_cases':1,'not_reached':15,'failed_case_number':34,'failed_case':'alma-linux-9-A-to-B-request-barrier','failure':d['failure'],'download_process_exit':0,'downloaded_version':'3.0.1','expected_barrier_snapshot':'A/3.0.0','http_serial':35,'command_index_zero_based':195,'baseline_installations_verified':8,'product_source':'fc18221282f289088ec9b4282fda8e0bfc67a3b8','reader_cleanup':f['reader_cleanup'],'cleanup':f['final_cleanup'],'cache_cleanup':d['cache_cleanup'],'actual_job_times':{k:jobs[0][k] for k in ('started_at','completed_at')},'driver_start':'2026-10-21T16:05:35Z','command_bound_seconds':3300,'kill_after_seconds':45,'job_bound_minutes':60},'causal_limit':'Driver, validator, fixtures, and native workflow bytes equal accepted PR74 tip and merged base. This is not proof of infrastructure causality. Actual sequence moved from A to B signed metadata then B signed RPM; exact-A host guard rejected it. No repair or CI rerun performed.','test_only':True,'published':False,'public_origin_verified':False,'source_provenance_verified':False,'reruns_dispatched':0,'production_mutations':0,'observer_exec_sessions':'all completed before sealing'}
(P/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
(P/'README.md').write_text('''# Packages PR #75 — first CI generation

Head `eac9d252f13ea201b405825e6303de703af08c16`, base `620bdfa1804c732dc1b3b072bb64dbe6f0ae0475`, reviewed tree `ca255a7d0fc27186a68f291ccf7ce70948a976d1`. All three executed jobs checked out synthetic merge `0c87528bd58a40413924b51cdc251c183382826c`, whose complete tree equals the reviewed head. Both natural workflows are terminal, attempt 1; no rerun, dispatch, cancellation, source edit, PR status edit or merge occurred.

Pages run 39631486007 passed 554 Linux Python tests, zero skips, and four publication-control tests. Pages deployment was skipped. Native run 39631486744 failed only its stable-client job 119330566284; original signed-repository job 119330566802 passed, including the complete build/sign/verify/install drill. Four original small receipts prove 7 metadata, 10 signing, 6 bootstrap, 31 candidate and 8 transport cases. Their prescribed secret/scratch cleanup flags are true.

The stable driver passed 33 cases and failed case 34, `alma-linux-9-A-to-B-request-barrier`; 15 of 49 were not reached. Its last download process (commands zero-based index195) exited0 in1.882 seconds, verified a signed3.0.1 RPM, then the host exact-A snapshot check rejected it with `downloaded rpm package is absent from the indexed snapshot`. HTTP serial35 records A mirrorlist and A metadata followed by B mirrorlist, B signed metadata, and B3.0.1 RPM HTTP200. The B payload SHA/size matches the B baseline. This is different from the earlier retained-payload404 failure; no unique root cause is inferred.

Both A3.0.0 and B3.0.1 baseline installations had already passed all four distributions, including the original server/wkcli identity, bench, query, migrate, invalid-input and read-only-data checks. Their actual source is TEST ONLY build `fc18221282f289088ec9b4282fda8e0bfc67a3b8`. These eight results do not turn the failed49-case suite green. Reader cleanup and final cleanup each report verified=true, errors=[], containers=[], networks=[]; all four cache removals report zero remaining paths.

Start with `summary.json`, `identity-proof-final.json`, `first-failure.json` (full safe command195/create args and the complete serial35 sequence), `eight-baseline-identities.json`, and `receipt-verification.json`. `verify-receipts.py` independently verifies the archived failure/partial success and can run offline; `receipt_audit_passed=true` means the evidence audit passed, not that CI passed. Complete public job logs are preserved; the original native log is losslessly compressed. The five small artifacts were downloaded completely with API size/SHA and all ZIP CRC checks, retaining only JSON/JSONL members, no package binaries.

`driver-source-comparison.json` binds driver/validator/fixture/native-workflow bytes across PR74 accepted tip, merged base and this head; all are identical. This excludes a direct change to those files, not indirect causation or environmental effects. PR75 remains blocked by the failed check. No acceptance gate was weakened.

This is TEST ONLY preparation. It neither publishes packages nor proves public-origin installation or live source provenance. Private raw downloads and stderr are outside the public directory (0700/0600); signed URL queries are excluded/redacted. `ARCHIVE_ALLOWLIST.txt`, `SHA256SUMS`, `CRC32SUMS`, and `archive-sha256.json` define this bounded archive. Previous generations were not changed.
''')
# Validate public payloads and checksum only explicit regular non-bytecode files.
exclude={'ARCHIVE_ALLOWLIST.txt','SHA256SUMS','CRC32SUMS','archive-sha256.json'}
files=[]
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
print(json.dumps({'payload_count':len(files),'payload_bytes':manifest['payload_bytes'],'manifest_sha256':hashlib.sha256((P/'archive-sha256.json').read_bytes()).hexdigest(),'ci_passed':False,'all_terminal':True}))
