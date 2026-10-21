#!/usr/bin/env python3
"""Validate already downloaded public JSON/log evidence; never execute candidate code."""
import datetime,hashlib,json,pathlib,subprocess
P=pathlib.Path(__file__).resolve().parent
ATT=P.parent;PKG=ATT.parent;FIRST=PKG/'pr76-ci-generation-1'
W=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-object-provider')
HEAD='dded26afa12188d5f1c036af4b9c06c3cbddb46d';BASE='1829c3813d6b21dcfeb1238a640604a4691815bd';TREE='e319ee72e8d30f012fd5e6206d131e2b75c47a09';MERGE='3e4b7945206faffc18d6023f2b50311d93bd4348';PRODUCT='fc18221282f289088ec9b4282fda8e0bfc67a3b8'
def digest(b):return hashlib.sha256(b).hexdigest()
def read(p):return json.loads(p.read_bytes())
def write(n,d):(P/n).write_text(json.dumps(d,indent=2,ensure_ascii=False)+'\n')
def facts(p):return {'path':str(p),'bytes':p.stat().st_size,'sha256':digest(p.read_bytes())}
def git(*a):return subprocess.run(['git','-C',str(W),*a],capture_output=True,check=True,timeout=15).stdout
now=datetime.datetime.now(datetime.timezone.utc).isoformat()
# Verify immutable prior payload and initial handoff; never rewrite them.
assert digest((FIRST/'final-manifest.json').read_bytes())=='e886bc88254f70e7d1cf60842a20199ec1b00711f75dfd3a57df0374dad94bbf'
first_manifest=read(FIRST/'final-manifest.json');assert len(first_manifest['files'])==71
for f in first_manifest['files']:
 p=FIRST/f['path'];assert p.stat().st_size==f['bytes'] and digest(p.read_bytes())==f['sha256'],str(p)
assert digest((FIRST/'public-evidence.tar.gz').read_bytes())=='220dca46914f6f46e7f9169a30fb2bcca839eb6c224cabc13007282a89229089'
stage=read(ATT/'stage-freeze-handoff.json')
for f in stage['files']:
 p=ATT/f['path'];assert p.stat().st_size==f['bytes'] and digest(p.read_bytes())==f['sha256'],str(p)
write('prior-evidence-preservation.json',{'recorded_at':now,'attempt1_payload_count':71,'attempt1_manifest':facts(FIRST/'final-manifest.json'),'attempt1_archive':facts(FIRST/'public-evidence.tar.gz'),'all_attempt1_payload_unchanged':True,'attempt2_stage_files_unchanged':len(stage['files']),'stage_manifest':facts(ATT/'stage-freeze-handoff.json')})
r=read(P/'artifacts/native-stable-clients.json');old=read(FIRST/'artifacts/native-stable-four-clients-test-only/native-stable-clients.json')
assert r['passed'] is True and r['test_only'] is True and all(r[x] is False for x in ['public_origin_verified','published','source_provenance_verified'])
assert len(r['cases'])==49 and len({x['case'] for x in r['cases']})==49 and all(x['passed'] is True for x in r['cases'])
expected=read(PKG/'pr75-ci-generation-3/artifacts/native-stable-four-clients-test-only/native-stable-clients.json')
assert [(x['case'],x['expected_rejection']) for x in r['cases']]==[(x['case'],x['expected_rejection']) for x in expected['cases']]
assert r['control']=={'checkout_sha':MERGE,'tree':TREE}
merge=read(P/'merge-commit.json');assert merge['sha']==MERGE and merge['tree']['sha']==TREE and [v['sha'] for v in merge['parents']]==[BASE,HEAD]
assert MERGE in (P/'stable-job.log').read_text()
freeze=read(PKG/'provider-adapter-rebase-1829c381/source-freeze.json')
assert git('rev-parse','HEAD').decode().strip()==HEAD and git('rev-parse','HEAD^{tree}').decode().strip()==TREE and git('status','--porcelain=v1')==b''
for f in freeze['files']:
 raw=git('show',HEAD+':'+f['path']);assert len(raw)==f['bytes'] and digest(raw)==f['sha256']
assert sorted(git('diff','--name-only',BASE,HEAD).decode().splitlines())==sorted(f['path'] for f in freeze['files'])
source={'recorded_at':now,'head':HEAD,'base':BASE,'actual_checkout':MERGE,'tree':TREE,'parents':[BASE,HEAD],'reviewed_source_tree_matches':True,'all_eight_source_hashes_match_rebase_freeze':True,'source_files':freeze['files'],'local_worktree_clean':True,'source_freeze':facts(PKG/'provider-adapter-rebase-1829c381/source-freeze.json'),'rebase_review':facts(PKG/'provider-adapter-rebase-independent-review/review.json'),'spec_v2_review':facts(PKG/'provider-adapter-review-v2/spec/report.md'),'standards_v2_review':facts(PKG/'provider-adapter-review-v2/standards/report.md')}
write('source-binding.json',source)
clients={g:read(P/f'artifacts/clients-{g}.json') for g in ('A','B')}
fixtures=json.loads(git('show',HEAD+':tests/fixtures/wkcli-acceptance/sha256.json'))
identities=[]
for g,version in [('A','3.0.0'),('B','3.0.1')]:
 c=clients[g];case=next(x for x in r['cases'] if x['case']==f'baseline-{g}-four-download-and-installed-cli');raw=(P/f'artifacts/clients-{g}.json').read_bytes()
 assert case['result']=={'size':len(raw),'sha256':digest(raw)}
 assert c['expected_source_sha']==PRODUCT and c['expected_version']==version and c['installed_cli_verified'] is True and c['expected_version_verified'] is True and c['bootstrap_verified'] is True and c['test_only'] is True
 assert c['github_provenance_verified'] is False and c['publication_verified'] is False
 assert c['transport_inputs_sha256']==r['transport'][g]['inputs']['sha256'] and c['transport_plan_sha256']==r['transport'][g]['plan_sha256'] and c['snapshot_sha256']==r['transport'][g]['candidate']['snapshot_sha256']
 for fam in ('apt','rpm'):
  assert len(c[fam])==2
  for item in c[fam]:
   cli=item['installed_cli'];identity={'build_source':'release','commit':PRODUCT,'version':version}
   assert cli['identity']==identity and cli['verified'] is True and cli['fixtures']==fixtures
   assert cli['checks']==['version','help','bench_validate','db_query','migrate_diagnose','invalid_inputs','read_only_data']
   identities.append({'generation':g,'distribution':item['distribution'],'image':item['image'],'identity':identity,'checks':cli['checks'],'fixtures_match_reviewed':True,'download':item['download']})
assert len(identities)==8
write('installed-cli-proof.json',{'identities':identities,'count':8,'all_verified':True,'fresh_normal_dependency_network':r['a_b']['installed_dependencies_use_normal_network']})
cache=[];barrier=[];requestlog=[json.loads(x) for x in (P/'artifacts/https-requests.jsonl').read_bytes().splitlines()]
for c in r['cases']:
 name=c['case'];result=c.get('result',{})
 if '-cache-' in name:
  detail=result['cache'];generation='B' if name.endswith('cache-B-target') else 'A'
  distribution=name.split('-cache-')[0];entry=next(x for f in ('apt','rpm') for x in clients[generation][f] if x['distribution']==distribution)
  assert result['download']['sha256']==entry['download']['sha256'] and result['download']['size']==entry['download']['size']
  required=name.endswith('cache-A-repeat');assert detail['reuse_required']==required
  if required:
   assert detail['metadata_reads']
   for path in detail['metadata_reads']:
    before=detail['before'][path];after=detail['after'][path]
    assert after['atime_ns']>1 and all(before[k]==after[k] for k in ('device','inode','sha256','size'))
  cache.append({'case':name,'verified':True,'same_generation_reuse_required':required,'metadata_read_paths':detail['metadata_reads'],'target_download_sha256':result['download']['sha256']})
 if name in ['rocky-linux-9-A-to-B-request-barrier','alma-linux-9-A-to-B-request-barrier']:
  req=result['requests'];serials={v['serial'] for v in req};assert len(serials)==1;serial=next(iter(serials))
  assert req==[q for q in requestlog if q['serial']==serial]
  prefix='/snapshots/stable/r'+str(clients['B']['audit_release_id'])+'-'+clients['B']['snapshot_sha256']+'/site/rpm/stable/el/9/x86_64/'
  raw=('https://packages.githubim.com'+prefix+'\n').encode()
  positions=[i for i,x in enumerate(req) if x['path']=='/rpm/stable/el/9/x86_64/mirrorlist' and x['sha256']==digest(raw) and x['size']==len(raw) and x['response_write_completed'] is True]
  assert positions
  payloads=[(i,x) for i,x in enumerate(req) if x['path'].startswith(prefix+'Packages/') and i>positions[0]];assert payloads
  distribution=name.split('-A-to-B')[0];entry=next(x for x in clients['A']['rpm'] if x['distribution']==distribution)
  for i,x in payloads:assert x['sha256']==entry['download']['sha256'] and x['size']==entry['download']['size'] and x['status']==200 and x['response_write_completed'] is True
  assert all(x['source']['audit_release_id']==clients['A']['audit_release_id'] and x['retained_alias'] is True for x in result['rpm_requests']['payloads'])
  barrier.append({'case':name,'serial':serial,'A_audit_release_id':clients['A']['audit_release_id'],'B_audit_release_id':clients['B']['audit_release_id'],'B_mirror_position':positions[0],'B_prefix_exact_A_payload_positions':[i for i,x in payloads],'exact_A_sha256':entry['download']['sha256'],'all_response_writes_complete':True,'matches_raw_request_log':True})
assert len(cache)==12 and sum(c['same_generation_reuse_required'] for c in cache)==4 and len(barrier)==2
for key in ('cleanup','reader_cleanup'):
 assert r[key]['verified'] is True and r[key]['errors']==[] and r[key]['remaining']=={'containers':[],'networks':[]}
assert len(r['cache_cleanup'])==4 and all(x['verified'] is True and x['result']['remaining']==[] for x in r['cache_cleanup'])
assert all(not c['timed_out'] and not c['output_limit'] for c in r['commands'])
for c in r['cases']:
 if c['expected_rejection'] and 'exit_code' in c.get('result',{}):assert c['result']['exit_code'] not in (0,124,125,126,127,137,143)
write('cache-and-barrier-proof.json',{'cache_cases':cache,'same_generation_read_proofs':4,'RPM_request_barriers':barrier,'cache_cleanup':r['cache_cleanup'],'raw_request_count':len(requestlog)})
write('cleanup-proof.json',{'reader_cleanup':r['reader_cleanup'],'final_cleanup':r['cleanup'],'cache_cleanup':r['cache_cleanup'],'all_commands_within_driver_bounds':True})
inst=[(i,c) for i,c in enumerate(r['commands']) if c['stage']=='installed-client'];assert len(inst)==11 and all(c['exit_code']==0 for i,c in inst[:8]) and all(c['exit_code']==1 for i,c in inst[8:])
assert all('600s' in r['commands'][i-1]['command'] for i,c in inst)
comparison={'first_attempt':{'case':old['cases'][0]['case'],'passed_cases':0,'failed_cases':1,'not_reached':48,'Alma_installed_command':old['commands'][66]},'second_attempt':{'passed_cases':49,'failed_cases':0,'not_reached':0,'Alma_A_installed_command':r['commands'][66],'Alma_B_installed_command':r['commands'][93]},'inner_deadline_600s_unchanged_all_11_install_sandboxes':True,'test_only_fixture_regenerated':True,'causal_limit':'Attempt 2 did not reproduce the deadline. This does not establish a unique network/root cause or erase first failure.'}
write('attempt-boundary-comparison.json',comparison)
oldjobs=read(FIRST/'native-jobs-final.json')['jobs'];jobs=read(P/'jobs-attempt2.json')['jobs'];success=next(x for x in jobs if x['id']==119391228348);prior=next(x for x in oldjobs if x['id']==119376462664)
assert all(success[k]==prior[k] for k in ['started_at','completed_at','runner_id','runner_name','steps'])
assert (P/'retained-signed-job.log').read_bytes()==(FIRST/'job-119376462664.log').read_bytes()
write('retained-job-proof.json',{'old_job_id':119376462664,'current_projection_id':119391228348,'same_runner_timestamps_steps':True,'complete_log_byte_identical':True,'started_at':success['started_at'],'completed_at':success['completed_at'],'no_new_execution':True})
for f in ('pages-job.log','sdk-job.log'):
 (P/f).write_bytes((FIRST/f).read_bytes())
checks=read(P/'head-check-runs.json')['check_runs'];assert len(checks)==5 and all(c['status']=='completed' and c['conclusion']==('skipped' if c['name']=='Deploy complete Pages snapshot' else 'success') for c in checks)
run=read(P/'run-final.json');assert run['run_attempt']==2 and run['head_sha']==HEAD and run['status']=='completed' and run['conclusion']=='success'
pr=read(P/'pr-final.json');assert pr['head']['sha']==HEAD and pr['base']['sha']==BASE and pr['draft'] is True and pr['merged'] is False
write('summary.json',{'recorded_at':now,'run':39644526993,'attempt':2,'conclusion':'success','stable_job':119391226142,'head':HEAD,'actual_checkout':MERGE,'tree':TREE,'stable_cases':{'expected':49,'passed':49,'failed':0,'not_reached':0,'negative_cases':sum(c['expected_rejection'] for c in r['cases'])},'installed_cli_acceptances':8,'cache_cases':12,'same_generation_cache_read_proofs':4,'cache_directories_removed':4,'reader_cleanup_verified':True,'final_cleanup_verified':True,'control_tree_mutation_check':'success','Pages_inherited_attempt1':{'tests':567,'skips':0,'contracts':4},'SDK_inherited_attempt1':{'SDK':22,'inputs':10,'skips':0},'signed_native_inherited_attempt1_success':True,'latest_check_runs':[{k:c[k] for k in ('id','name','status','conclusion')} for c in checks],'CI_merge_assessment':'CI evidence supports merging this exact previously reviewed head/tree under the existing parent authorization. No new CI blocker is found; this is not a production release/activation verdict and collector does not merge.','history_retained':['Attempt1 failed at first baseline; 0 passed/1 failed/48 unreached.','First Alma deadline root cause remains unknown; attempt2 success is not first-attempt success.','One historical Standards P2 remains: two original HTTP wiring unit tests followed implementation.'],'not_claimed':['production activation or live provider success','complete new D2 production-shaped v2/native-signature integration','public release publication/origin acceptance'],'all_collection_commands_terminal':True,'no_third_attempt':True,'no_source_change_or_push_or_merge':True})
print(json.dumps({'evidence_verified':True,'cases':49,'installed':8,'cache_cases':12,'reuse_proofs':4,'prior71_payload_unchanged':True,'stage12_unchanged':True,'CI_supports_exact_reviewed_head':True}))
