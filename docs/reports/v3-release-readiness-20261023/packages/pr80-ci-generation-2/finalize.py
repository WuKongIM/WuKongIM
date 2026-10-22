from pathlib import Path
import datetime,hashlib,json,re
r=Path(__file__).resolve().parent
head='b8fc6d5e384630ef1d4c0f00281a836d115456e8'
checkout='7bb52a0d677c5349453c0004d73ffecf09ad47d6'
base='721712e6d77c5f159fff94dcdb2bd1fb84ad464c'
snap=sorted((r/'snapshots').iterdir())[-1]
s=json.loads((snap/'summary.json').read_text())
assert s['head']==head and len(s['runs'])==3
assert all(x['status']=='completed' and x['conclusion']=='success' and x['attempt']==1 for x in s['runs'])
assert len(s['checks'])==5 and sum(x['conclusion']=='success' for x in s['checks'])==4 and sum(x['conclusion']=='skipped' for x in s['checks'])==1
assert json.loads((snap/'pr.json').read_text())['head']['sha']==head
identity=json.loads((r/'source-identity.json').read_text())
assert identity['head']==head and identity['actual_checkout']==checkout and identity['tree_equal']
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def load(name):
 paths=list((r/'artifacts').glob('*/'+name));assert len(paths)==1,(name,paths)
 p=paths[0];return p,json.loads(p.read_text())
receipts=[];command_records=[]
positive=[('native-source-metadata.json',7),('native-local-signing.json',10),('native-bootstrap-preparation.json',6),('native-stable-candidate.json',31),('result.json',29),('native-preparation.json',2),('native-sdk-result.json',20),('native-stable-clients.json',49)]
for name,count in positive:
 p,d=load(name);assert d['passed'] is True and len(d['cases'])==count and all(c['passed'] is True for c in d['cases'])
 if name not in {'native-source-metadata.json','native-local-signing.json','native-stable-clients.json'}:assert d.get('source_sha')==checkout
 receipts.append({'path':p.relative_to(r).as_posix(),'passed':True,'cases':count,'source_sha':d.get('source_sha'),'source_binding':'typed_source_sha' if d.get('source_sha')==checkout else 'actual_workflow_checkout_and_fixed_step','scratch_removed':d.get('scratch_removed'),'sha256':sha(p)})
 for index,c in enumerate(d.get('commands',[])):
  rec={'receipt':p.relative_to(r).as_posix(),'command_index':index,'stage':c.get('stage'),'exit_code':c.get('exit_code'),'timed_out':c.get('timed_out')}
  if 'output_size' in c:
   b=c['output_tail'].encode();full=len(b)==c['output_size'];match=hashlib.sha256(b).hexdigest()==c['output_sha256'] if full else None
   if full:assert match
   rec.update(output_contract='merged_output_size_hash_tail',output_size=c['output_size'],retained_tail_bytes=len(b),full_output_available=full,sha256_recomputed=match)
  elif isinstance(c.get('stdout'),dict) and isinstance(c.get('stderr'),dict):rec.update(output_contract='separate_stream_size_hash_only',stdout=c['stdout'],stderr=c['stderr'],full_output_available=False,sha256_recomputed=None)
  elif 'stdout_tail' in c or 'stderr_tail' in c:rec.update(output_contract='separate_stream_tail_only',stdout_tail_bytes=len(c.get('stdout_tail','').encode()),stderr_tail_bytes=len(c.get('stderr_tail','').encode()),full_output_available=False,sha256_recomputed=None)
  else:rec.update(output_contract='unrecognized',full_output_available=False,sha256_recomputed=None)
  command_records.append(rec)
_,prep=load('native-preparation.json');_,sdk=load('native-sdk-result.json');cleanup_path,cleanup=load('cleanup.json')
assert prep['state']=='native_prepared' and re.fullmatch('[0-9a-f]{64}',prep['bundle_sha256'])
assert sdk['test_only'] is True and sdk['source_sha']==checkout and sdk['skips']==0 and sdk['state']=='fixture_complete' and sdk['stage']=='sdk_fixture_complete' and sdk['scratch_removed'] is True
assert sdk['cleanup']=={'attempted':True,'removed':True}
assert sdk['network_guard']=={'passed':True,'self_check_attempts':1,'unexpected_attempts':0}
assert all(v is False for v in sdk['verification'].values())
assert cleanup=={'owned_container_removed':True,'owned_work_removed':True}
assert sdk['native_bindings']['export_source_sha']==checkout and sdk['native_bindings']['current_control']['source_sha']==checkout
assert sdk['object_operations']=={'A':153,'B':153,'shared_exact_conflicts':152,'unique_objects':154}
assert sdk['route_counts']=={'A':178,'B':178} and sdk['config_rule_counts']=={'A':17,'B':17}
_,stable=load('native-stable-clients.json')
assert stable['cleanup']['verified'] is True and stable['cleanup']['remaining']=={'containers':[],'networks':[]}
assert all(c['verified'] and not c['result']['remaining'] for c in stable['cache_cleanup'])
cli=[]
for label,version in [('A','3.0.0'),('B','3.0.1')]:
 _,d=load('clients-'+label+'.json');assert d['installed_cli_verified'] and d['expected_version_verified'] and d['expected_version']==version
 for family in ('apt','rpm'):
  for c in d[family]:
   target={'build_source':'release','commit':'fc18221282f289088ec9b4282fda8e0bfc67a3b8','version':version}
   assert c['installed_cli']['verified'] is True and c['installed_cli']['identity']==target
   cli.append({'generation':label,'distribution':c['distribution'],'identity':target,'download':c['download']})
assert len(cli)==8
logs={}
for p in (r/'logs').glob('*.log'):
 t=p.read_text();assert checkout in t
 logs[p.name]={'bytes':p.stat().st_size,'sha256':sha(p),'actual_checkout':checkout}
assert len(logs)==4
page_log=(r/'logs/job-120015025838.log').read_text();sdk_log=(r/'logs/job-120015025493.log').read_text();native_log=(r/'logs/job-120015026222.log').read_text()
assert 'Ran 595 tests in 133.053s' in page_log and 'Ran 4 tests' in page_log and 'OK (skipped=' not in page_log
assert 'Ran 49 tests in 1.342s' in sdk_log and 'Ran 18 tests in 4.050s' in sdk_log and 'OK (skipped=' not in sdk_log
markers=['unsigned native-package repositories built at','signed native-package repositories created at','native-package repository signatures and metadata closure validated','native-package signed repository container validation passed']
assert all(m in native_log for m in markers)
job_data=json.loads((snap/'39833326934-jobs.json').read_text());native_job=next(j for j in job_data['jobs'] if j['id']==120015026222)
required_steps=['Prepare verified native SDK object fixtures','Verify all native objects through the locked SDK HTTP fixture','Remove owned native SDK fixture work','Build unsigned TEST ONLY packages','Build, sign, verify, and install TEST ONLY repositories','Reject tracked-tree mutation']
steps=[x for x in native_job['steps'] if x['name'] in required_steps];assert len(steps)==len(required_steps) and all(x['conclusion']=='success' for x in steps)
artifacts=[]
for p in (r/'artifacts').glob('*-validation.json'):
 a=json.loads(p.read_text());assert a['api_sha256_matched'] and a['zip_crc_verified']
 zip_path=r/'artifacts'/(str(a['artifact_api']['id'])+'-'+a['artifact_api']['name']+'.zip')
 assert sha(zip_path)==a['local_sha256'] and zip_path.stat().st_size==a['downloaded_bytes']==a['artifact_api']['size_in_bytes']
 for m in a['members']:
  path=zip_path.with_suffix('')/m['name'];assert sha(path)==m['sha256'] and path.stat().st_size==m['bytes']
 artifacts.append({'id':a['artifact_api']['id'],'name':a['artifact_api']['name'],'bytes':a['downloaded_bytes'],'sha256':a['local_sha256'],'members':len(a['members'])})
assert len(artifacts)==8
old=r.with_name('pr80-ci-generation-1');baseline=json.loads((r/'baseline.json').read_text());assert sha(old/'SHA256SUMS')==baseline['previous_generation_manifest_sha256'] and sha(old/'archive-summary.json')==baseline['previous_generation_archive_summary_sha256']
for line in (old/'SHA256SUMS').read_text().splitlines():
 digest,name=line.split(None,1);assert sha(old/name)==digest
coverage={'command_records':len(command_records),'merged_size_hash_tail_records':sum(x['output_contract']=='merged_output_size_hash_tail' for x in command_records),'complete_merged_output_sha256_recomputed':sum(x.get('sha256_recomputed') is True for x in command_records),'merged_tails_with_declared_hash_only':sum(x['output_contract']=='merged_output_size_hash_tail' and not x['full_output_available'] for x in command_records),'separate_stream_size_hash_only_commands':sum(x['output_contract']=='separate_stream_size_hash_only' for x in command_records),'separate_stream_tail_only_commands':sum(x['output_contract']=='separate_stream_tail_only' for x in command_records),'unrecognized_commands':sum(x['output_contract']=='unrecognized' for x in command_records)}
summary={'schema':'wukongim/pr80-linux-ci-generation-2/v1','as_of_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'result':'passed_all_first_attempt_linux_checks','head':head,'base':base,'actual_checkout':checkout,'head_and_checkout_tree':identity['head_tree'],'final_observation':s,'pages':{'default_tests':595,'default_skips':0,'default_seconds':133.053,'contract_tests':4,'deploy':'conditional_skip'},'sdk_contract':{'tests':49,'skips':0,'input_tests':18,'input_skips':0},'positive_native_receipts':receipts,'new_native_sdk':{'preparation_cases':2,'preparation_bundle_sha256':prep['bundle_sha256'],'sdk_cases':20,'sdk_skips':0,'state':sdk['state'],'stage':sdk['stage'],'guard':sdk['network_guard'],'cleanup':sdk['cleanup'],'scratch_removed':True,'workflow_cleanup':cleanup,'workflow_cleanup_path':cleanup_path.relative_to(r).as_posix(),'object_operations':sdk['object_operations'],'route_counts':sdk['route_counts'],'config_rule_counts':sdk['config_rule_counts'],'all_verification_fields_false':True},'installed_clients':cli,'independent_stable_client_cleanup':{'verified':True,'owned_container_creation_attempts':len(stable['cleanup']['creation_attempts']['containers']),'owned_network_creation_attempts':len(stable['cleanup']['creation_attempts']['networks']),'remaining':stable['cleanup']['remaining'],'cache_cleanup_cases':len(stable['cache_cleanup'])},'full_signed_repository_installation':{'executed':True,'required_steps':steps,'log_markers':[x for x in native_log.splitlines() if any(m in x for m in markers)]},'job_logs':logs,'artifact_zips':{'count':len(artifacts),'artifacts':artifacts,'all_full_downloads':True,'all_api_sha256_matched':True,'all_member_crc_verified':True,'partial_downloads':0},'command_output_coverage':coverage,'previous_generation_manifest_unchanged':True,'source_changed':False,'dispatches':0,'reruns':0,'merges':0,'cloud_or_nas_or_key_access':False,'production_publication_verified':False,'limits':'These are TEST ONLY fixture and native-package regression checks. SDK fixture requests are offline and cannot prove live CDN/OSS or production publication. Legacy metadata/local-signing receipt schemas omit source_sha; their source binding is the verified workflow checkout and fixed commands, not an invented receipt field. Cleanup observations cover the named owned scratch, containers, networks and caches, not a host-wide process inventory. Merge requires the root operator fresh production-control-workflow quiet gate.'}
(r/'bounded-command-output-validation.json').write_text(json.dumps(command_records,indent=2)+'\n')
(r/'validation-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
(r/'README.md').write_text(f'''# packages PR #80 Linux CI after UID handoff repair\n\nThis archive freezes the first CI generation for reviewed head {head}, base {base}. Actual PR checkout {checkout} has the identical tree {identity['head_tree']}, verified through GitHub Git commit APIs and all four job logs. All three workflows are attempt 1; all jobs are terminal. Four checks passed and the conditional PR Pages deployment was skipped. No rerun, dispatch, source edit, push or merge was performed by this collector.\n\nThe Linux default suite passed 595 tests in 133.053 seconds, zero skips, plus four repository contracts. The official SDK suite passed 49 tests and input regressions passed 18 tests, zero skips. Existing native metadata/local-signing/bootstrap/candidate/delivery/stable-client suites passed 7/10/6/31/29/49 cases.\n\nThe new native SDK preparation passed both cases. Its official locked SDK fixture completed all 20 named scenarios, recorded fixture_complete, a one-attempt network-guard self-check with zero unexpected connections, and successful owned scratch removal. The separate workflow cleanup.json confirms owned work and container removal. A/B each exercised all 153 objects and 178 routes; both configurations contained 17 rules. All production, cloud, publication, source-authentication and live-equivalence verification flags remain false.\n\nThe unsigned package build and full signed-repository container installation really executed and passed: the full job log retains unsigned creation, signed creation, signature/metadata closure and final signed-repository validation markers. Independently, the stable four-client receipt passed all 49 cases with eight exact installed CLI identities across A=3.0.0 and B=3.0.1, Ubuntu/Debian/Rocky/AlmaLinux and fixed product source fc18221282f289088ec9b4282fda8e0bfc67a3b8. Its named owned containers/networks and four caches were observed removed.\n\nAll eight small public TEST ONLY artifact ZIPs were fully downloaded, compared against API SHA-256 and ZIP/member sizes, CRC-verified, and every member hashed. Command-output coverage distinguishes complete recomputed merged bytes from bounded tails or hash-only streams. The prior generation's manifest and all retained members were independently revalidated unchanged. That original PermissionError failure is not overwritten or relabelled as infrastructure.\n\nThese results do not establish production signing, cloud readiness, live CDN equivalence, public installed-client publication or v3 GA release. Merge is left to the root operator's fresh five-workflow production-control quiet gate.\n''')
files=sorted(p for p in r.rglob('*') if p.is_file() and p.name not in {'PUBLIC_ALLOWLIST.json','SHA256SUMS','archive-summary.json'})
assert all(not p.is_symlink() for p in files)
records=[{'path':p.relative_to(r).as_posix(),'size':p.stat().st_size,'sha256':sha(p)} for p in files]
(r/'PUBLIC_ALLOWLIST.json').write_text(json.dumps({'schema':'wukongim/public-evidence-allowlist/v1','files':records,'contains_private_material':False},indent=2)+'\n')
files=sorted(files+[r/'PUBLIC_ALLOWLIST.json'])
manifest=''.join(sha(p)+'  '+p.relative_to(r).as_posix()+'\n' for p in files).encode();(r/'SHA256SUMS').write_bytes(manifest)
a={'payload_files':len(records),'payload_bytes':sum(v['size'] for v in records),'manifest_entries':len(files),'manifest_sha256':hashlib.sha256(manifest).hexdigest(),'result':summary['result']}
(r/'archive-summary.json').write_text(json.dumps(a,indent=2)+'\n');print(json.dumps(a));print(json.dumps(coverage))
