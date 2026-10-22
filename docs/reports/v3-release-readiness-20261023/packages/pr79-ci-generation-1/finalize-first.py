from pathlib import Path
import datetime,hashlib,json,re
r=Path(__file__).resolve().parent
snap=sorted((r/'snapshots').iterdir())[-1]
s=json.loads((snap/'summary.json').read_text()); assert all(x['status']=='completed' for x in s['runs'])
assert [x['conclusion'] for x in s['runs']]==['failure','success','success']
def load_member(name):
 paths=list((r/'artifacts').glob('*/'+name));assert len(paths)==1;return paths[0],json.loads(paths[0].read_text())
receipts=[];cmds=[]
for name,count in [('native-source-metadata.json',7),('native-local-signing.json',10),('native-bootstrap-preparation.json',6),('native-stable-candidate.json',31),('result.json',29),('native-stable-clients.json',49)]:
 p,d=load_member(name);assert d['passed'] and len(d['cases'])==count and all(c['passed'] for c in d['cases'])
 receipts.append({'path':p.relative_to(r).as_posix(),'cases':count,'passed':True,'source_sha':d.get('source_sha'),'scratch_removed':d.get('scratch_removed'),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()})
 for c in d.get('commands',[]):
  if 'output_size' not in c:continue
  b=c['output_tail'].encode();full=len(b)==c['output_size'];match=hashlib.sha256(b).hexdigest()==c['output_sha256'] if full else None
  if full:assert match
  cmds.append({'receipt':p.relative_to(r).as_posix(),'stage':c['stage'],'exit_code':c['exit_code'],'timed_out':c['timed_out'],'output_limit':c['output_limit'],'output_size':c['output_size'],'retained_tail_bytes':len(b),'complete_output_available':full,'sha256_recomputed':match})
p,stable=load_member('native-stable-clients.json'); cli=[]
assert stable['cleanup']['verified'] and stable['cleanup']['remaining']=={'containers':[],'networks':[]}
assert all(c['verified'] and not c['result']['remaining'] for c in stable['cache_cleanup'])
for label,ver in [('A','3.0.0'),('B','3.0.1')]:
 p,d=load_member('clients-'+label+'.json');assert d['installed_cli_verified'] and d['expected_version_verified'] and d['expected_version']==ver
 for family in ('apt','rpm'):
  for c in d[family]:
   assert c['installed_cli']['verified'] and c['installed_cli']['identity']=={'build_source':'release','commit':'fc18221282f289088ec9b4282fda8e0bfc67a3b8','version':ver}
   cli.append({'generation':label,'distribution':c['distribution'],'identity':c['installed_cli']['identity'],'download':c['download']})
logs={}
for p in (r/'logs').glob('*.log'):
 t=p.read_text();assert 'f1c75e55e445aa40076f20586fd2481266ee73ff' in t
 logs[p.name]={'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'actual_checkout':'f1c75e55e445aa40076f20586fd2481266ee73ff'}
validation={'schema':'wukongim/pr79-first-ci-validation/v1','as_of_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'overall':'incomplete_first_attempt_failure','head':s['head'],'base':'7255102de81b45c23d52e908ccdde1b7799ee3f5','actual_checkout':'f1c75e55e445aa40076f20586fd2481266ee73ff','head_and_merge_tree':'306e2cd808742a9effb981fd679d2153cbcf29a4','first_attempt_terminal_snapshot':snap.relative_to(r).as_posix(),'runs':s['runs'],'check_conclusions':{k:sum(c['conclusion']==k for c in s['checks']) for k in ['success','failure','skipped']},'pages_default_tests':{'tests':587,'skips':0,'seconds':130.431,'contract_tests':4},'sdk_contract':{'tests':34,'skips':0,'input_tests':10},'native_receipts':receipts,'installed_clients':cli,'cleanup':{'verified':stable['cleanup']['verified'],'owned_container_creation_attempts':len(stable['cleanup']['creation_attempts']['containers']),'owned_network_creation_attempts':len(stable['cleanup']['creation_attempts']['networks']),'remaining':stable['cleanup']['remaining'],'cache_cleanup_cases':len(stable['cache_cleanup']),'host_wide_process_inventory_claimed':False},'job_logs':logs,'artifacts':{'count':len(list((r/'artifacts').glob('*-validation.json'))),'all_downloaded_zip_sha256_matches_api':True,'all_downloaded_zip_crc_verified':True,'partial_zip_downloads':0},'command_outputs':{'records':len(cmds),'full_output_sha256_recomputed':sum(c['complete_output_available'] for c in cmds),'tails_only_hash_declared':sum(not c['complete_output_available'] for c in cmds)},'full_signed_repository_installation_executed':False,'source_modified':False,'rerun_performed_in_this_archive':False,'production_publication_verified':False}
(r/'bounded-command-output-validation.json').write_text(json.dumps(cmds,indent=2)+'\n')
(r/'validation-summary.json').write_text(json.dumps(validation,indent=2)+'\n')
(r/'README.md').write_text('''# packages PR #79 first Linux CI attempt\n\nThis archive preserves all three first-run workflows at head b100f91a59902ad8d93b0632f9c84f3f7468e5f2. All are attempt 1. The actual checkout f1c75e55e445aa40076f20586fd2481266ee73ff has the same tree 306e2cd808742a9effb981fd679d2153cbcf29a4 as the PR head.\n\nThe first attempt is **incomplete**: the signed-repository job failed installing the fixed GoReleaser tool because proxy.golang.org returned an HTTP/2 INTERNAL_ERROR while downloading golang.org/x/tools v0.49.0. The unsigned product build and complete signed repository installation were skipped. See first-failure.json and the original full job log; this failure has not been overwritten or reported as green.\n\nPages passed 587 default tests, zero skips, and four contract tests; Pages configuration/upload and deployment were conditionally skipped. SDK passed 34 contracts plus 10 input tests, zero skips. The original native metadata/local-signing/bootstrap/candidate/delivery suites passed 7/10/6/31/29 cases. The independent stable-client job passed all 49 cases, including eight installed CLI identities across A=3.0.0 and B=3.0.1 on Ubuntu, Debian, Rocky and AlmaLinux; each identity is source fc18221282f289088ec9b4282fda8e0bfc67a3b8 and build_source=release. Cleanup receipts show zero remaining owned containers/networks and empty owned caches. They do not claim a host-wide process inventory.\n\nSeven bounded public artifact ZIPs were fully downloaded, checked against the GitHub API SHA-256 digest, and CRC-checked for every member before extraction. Command receipts retain bounded tails; bounded-command-output-validation.json distinguishes locally recomputed full-output hashes from declared hashes whose full stream was not retained. The archive contains no production private keys or private cloud bundle. TEST ONLY results do not establish fresh GitHub production authorization, public stable origin, activation, or publication.\n\nAfter the workflow reached terminal state, root explicitly permitted one separate retry of only failed job 119973638102, keeping source, workflow, pins, thresholds and deadlines unchanged. That decision is recorded here; execution and later evidence belong in the separate sibling pr79-native-infra-retry-1 archive. This archive contains no retry execution.\n''')
excluded={'PUBLIC_ALLOWLIST.json','SHA256SUMS','archive-summary.json'}
files=sorted(p for p in r.rglob('*') if p.is_file() and p.name not in excluded)
records=[{'path':p.relative_to(r).as_posix(),'size':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} for p in files]
allow={'schema':'wukongim/public-evidence-allowlist/v1','files':records,'contains_private_material':False}
(r/'PUBLIC_ALLOWLIST.json').write_text(json.dumps(allow,indent=2)+'\n')
files.append(r/'PUBLIC_ALLOWLIST.json');files=sorted(files)
raw=''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.relative_to(r).as_posix()+'\n' for p in files).encode();(r/'SHA256SUMS').write_bytes(raw)
summary={'payload_files':len(records),'payload_bytes':sum(v['size'] for v in records),'manifest_entries':len(files),'manifest_sha256':hashlib.sha256(raw).hexdigest(),'overall':'incomplete_first_attempt_failure'}
(r/'archive-summary.json').write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps(summary));print(json.dumps(validation['command_outputs']))
