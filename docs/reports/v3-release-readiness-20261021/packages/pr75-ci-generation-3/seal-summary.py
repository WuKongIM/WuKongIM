from pathlib import Path
import json,hashlib,gzip,zlib,re,datetime
P=Path(__file__).resolve().parent
R=lambda n:json.loads((P/n).read_text())
e=R('expected.json');v=R('receipt-verification.json');pr=R('pr-final.json');m=R('merge-commit.json')
assert v['receipt_audit_passed'] and v['all_observed_ci_passed']
assert pr['head']['sha']==e['head'] and pr['base']['sha']==e['base']
assert pr['draft'] and pr['state']=='open'
logs=['pages-job.log','legacy-native-job.log.gz','stable-job.log']
assert all(m['sha'] in (gzip.decompress((P/n).read_bytes()).decode() if n.endswith('.gz') else (P/n).read_text()) for n in logs)
identity={'head':e['head'],'base':e['base'],'head_tree':e['tree'],'synthetic_checkout':m['sha'],'synthetic_tree':m['tree']['sha'],'merge_parents':[x['sha'] for x in m['parents']],'all_three_job_logs_contain_actual_checkout':logs,'stable_receipt_control':{'checkout_sha':m['sha'],'tree':e['tree']},'head_unchanged_after_terminal':True}
(P/'identity-proof-final.json').write_text(json.dumps(identity,indent=2)+'\n')
(P/'eight-baseline-identities.json').write_text(json.dumps(v['baseline'],indent=2)+'\n')
(P/'rpm-barrier-proof.json').write_text(json.dumps(v['rpm_switch'],indent=2)+'\n')
summary={'pr':'https://github.com/WuKongIM/packages/pull/75','head':e['head'],'base':e['base'],'tree':e['tree'],'attempt':1,'pr_draft':pr['draft'],'all_runs_terminal':True,'all_observed_checks_passed':True,'pages':R('pages-verification.json'),'native':{'run':e['native_run'],'conclusion':'success','legacy_cases':v['legacy'],'complete_signed_repository_build_verify_install_passed':True},'stable':{'run':e['native_run'],'job':119365179474,'cases':49,'passed':49,'failed':0,'not_reached':0,'installed_identities':8,'product_source':'fc18221282f289088ec9b4282fda8e0bfc67a3b8','rpm_barrier_cases':[{'case':x['case'],'serial':x['serial'],'download':x['download'],'both_dnf_invocations_metadata_expire_minus_one':True,'selected_A_metadata_before_B_mirrorlist':True,'B_prefix_exact_A_payload':True} for x in v['rpm_switch']],'cache_scenarios':len(v['cache']),'same_generation_reuse_proofs':sum(x['reuse_required'] for x in v['cache']),'cleanup':v['cleanup']},'test_only':True,'published':False,'public_origin_verified':False,'source_provenance_verified':False,'reruns_dispatched':0,'source_changes':0,'cloud_actions':0,'limitations':v['limitations']+['Generation 1 and 2 first failures remain retained unchanged.'],'observer_exec_sessions':'all completed before sealing'}
(P/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
(P/'README.md').write_text(f'''# Packages PR #75 — third CI generation

Head `{e['head']}`, base `{e['base']}`, tree `{e['tree']}`. All three executed jobs checked out synthetic merge `{m['sha']}` with the complete accepted tree. Both natural workflows finished successfully on attempt 1; the PR was still draft at the final observation. The observer did not dispatch, rerun, cancel, edit source/PR, merge, access cloud resources or production keys.

Pages run {e['pages_run']} passed 557 Linux Python tests with zero skips and four publication-control tests. Configure Pages, artifact upload and deployment were skipped. Native run {e['native_run']} passed the full signed repository build/verify/install drill, plus 7 metadata, 10 local-signing, 6 bootstrap, 31 stable-candidate and 8 transport cases. All 49 new stable-client scenarios passed, with no unexecuted scenario.

Eight A/3.0.0 and B/3.0.1 installed identities cover Ubuntu24.04, Debian13, Rocky9 and Alma9. They bind TEST ONLY product source `fc18221282f289088ec9b4282fda8e0bfc67a3b8` and the original CLI checks. Both RPM request barriers independently prove one serial, selected A metadata, the exact B mirrorlist body digest, then a B-prefixed retained alias returning the exact A payload hash/size. Both native DNF invocations include metadata_expire=-1; the first still performs makecache --refresh. The full command and request receipts are in `rpm-barrier-proof.json`.

Twelve persistent-cache scenarios include four actual same-generation reuse proofs (same device/inode/content and post-arming read evidence). Reader and final container/network inventories are empty without cleanup errors; all four owned cache roots were removed. No cache-policy or failure assertion was changed by the observer.

Start with `summary.json`, `identity-proof-final.json`, `rpm-barrier-proof.json`, `eight-baseline-identities.json` and `receipt-verification.json`. Run `python3 verify-receipts.py` inside this copied evidence directory to reproduce the offline receipt assertions. All five small artifacts were downloaded completely and checked against API size/SHA-256 and every ZIP member CRC. Only JSON/JSONL members are public. Full safe job logs are retained, with the large legacy log losslessly compressed. Raw downloads and stderr live in a private sibling directory outside the allowlist; no binaries, native packages or private keys are archived publicly.

Earlier failed generations remain unchanged. These results are TEST ONLY local-origin acceptance, not public publication, live provenance, public origin/TLS acceptance or global CDN atomicity. Original repomd bytes are not in these small artifacts: this archive retains their signed-drill and HTTP/hash receipts, not standalone metadata files.
''')
exclude={'ARCHIVE_ALLOWLIST.txt','SHA256SUMS','CRC32SUMS','archive-sha256.json'};files=[]
for p in sorted(P.rglob('*')):
 if not p.is_file() or '__pycache__' in p.parts or p.name in exclude:continue
 assert not p.is_symlink();raw=p.read_bytes();b=gzip.decompress(raw) if p.suffix=='.gz' else raw
 assert not re.search(rb'-----BEGIN (?:OPENSSH |RSA |EC |DSA )?PRIVATE KEY|github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,}',b),p
 if p.suffix=='.json':json.loads(b)
 if p.suffix=='.jsonl':
  for line in b.splitlines():json.loads(line)
 files.append({'path':str(p.relative_to(P)),'bytes':len(raw),'sha256':hashlib.sha256(raw).hexdigest(),'crc32':format(zlib.crc32(raw)&0xffffffff,'08x')})
(P/'ARCHIVE_ALLOWLIST.txt').write_text(''.join(x['path']+'\n' for x in files))
(P/'SHA256SUMS').write_text(''.join(x['sha256']+'  '+x['path']+'\n' for x in files))
(P/'CRC32SUMS').write_text(''.join(x['crc32']+'  '+x['path']+'\n' for x in files))
manifest={'schema':'public-evidence-allowlist/v1','frozen_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'head':e['head'],'tree':e['tree'],'payload_count':len(files),'payload_bytes':sum(x['bytes'] for x in files),'files':files}
(P/'archive-sha256.json').write_text(json.dumps(manifest,indent=2)+'\n')
for x in files:assert hashlib.sha256((P/x['path']).read_bytes()).hexdigest()==x['sha256']
print(json.dumps({'all_terminal':True,'ci_passed':True,'payload_count':len(files),'payload_bytes':manifest['payload_bytes'],'manifest_sha256':hashlib.sha256((P/'archive-sha256.json').read_bytes()).hexdigest()}))
