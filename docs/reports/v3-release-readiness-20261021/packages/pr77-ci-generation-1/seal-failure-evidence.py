from pathlib import Path
import ast,collections,datetime,gzip,hashlib,json,re,shutil,subprocess,zlib
P=Path(__file__).resolve().parent;ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-preview-fixture')
R=lambda n:json.loads((P/n).read_bytes());sha=lambda b:hashlib.sha256(b).hexdigest()
def facts(p):
 b=p.read_bytes();return {'path':str(p),'size':len(b),'sha256':sha(b)}
def put(name,value):(P/name).write_text(json.dumps(value,indent=2,sort_keys=True)+'\n')
def git(path,rev):return subprocess.check_output(['git','show',rev+':'+path],cwd=ROOT)
e=R('expected.json');a=R('actual-checkout-e306351.json');h=R('head-commit.json');pr=R('pr-final.json')
assert pr['head']['sha']==e['head'] and pr['base']['sha']==e['base'] and pr['draft'] and pr['state']=='open'
assert h['sha']==e['head'] and h['tree']['sha']==a['tree']['sha']==e['tree']
assert [x['sha'] for x in a['parents']]==[e['base'],e['head']]
for label in ['native','pages','sdk']:
 run=e[label+'_run']
 for part in ['run','jobs','artifacts']:
  paths=sorted(P.glob(f'snapshots/*/{run}-{part}.json'));value=json.loads(paths[-1].read_bytes())
  if part=='run':
   assert value['head_sha']==e['head'] and value['run_attempt']==1 and value['event']=='pull_request' and value['status']=='completed'
   assert value['conclusion']==('failure' if label=='native' else 'success')
  (P/(label+'-'+part+'-final.json')).write_bytes(paths[-1].read_bytes())
D=R('artifacts/native-stable-four-clients-test-only/native-stable-clients.json')
assert D['control']=={'checkout_sha':a['sha'],'tree':e['tree']} and D['passed'] is False
assert D['phase']=='baseline-A-four-download-and-installed-cli' and len(D['cases'])==1 and not D['cases'][0]['passed']
assert D['fixture']['secret_tree_removed'] and D['fixture']['data_only'] is False and D['fixture']['source_sha']=='fc18221282f289088ec9b4282fda8e0bfc67a3b8'
assert D['product_build']['source_sha']==R('product-source-commit.json')['sha'] and D['product_build']['tree']==R('product-source-commit.json')['tree']['sha']
for k in ['reader_cleanup','cleanup']:assert D[k]['verified'] and D[k]['errors']==[] and D[k]['remaining']=={'containers':[],'networks':[]}
assert D['cache_cleanup']==[]
failed=[(i,c) for i,c in enumerate(D['commands']) if c['exit_code']!=0];assert len(failed)==1
index,c=failed[0];assert index==66 and c['stage']=='installed-client' and c['exit_code']==124 and not c['timed_out'] and not c['output_limit']
raw=c['output_tail'].encode();assert len(raw)==c['output_size']==137 and sha(raw)==c['output_sha256'];(P/'first-failure-output.txt').write_bytes(raw)
create=D['commands'][65];assert create['stage']=='create-container' and create['output_tail'].strip()==c['command'][-1]
assert create['command'][-6:-1]==['timeout','--kill-after=10s','600s','bash','-euc']
script=create['command'][-1]
sequence=['sha256sum --check --strict','rpm --import /reviewed-key.asc','rpm --checksig /candidate.rpm','dnf --setopt=localpkg_gpgcheck=1 --setopt=timeout=60 install -y python3 /candidate.rpm','python3 /acceptance.py']
assert [script.index(x) for x in sequence]==sorted(script.index(x) for x in sequence)
assert '>/evidence/installed-cli.json' in script
requests=[json.loads(l) for l in (P/'artifacts/native-stable-four-clients-test-only/https-requests.jsonl').read_text().splitlines()]
assert len(requests)==33 and {r['serial'] for r in requests}=={2} and all(r['status']==200 and r['response_write_completed'] for r in requests)
assert all(r['mode']=='normal' for r in requests)
prior_installed=[{'index':i,**{k:x[k] for k in ['stage','exit_code','elapsed_seconds','timed_out','output_sha256']}} for i,x in enumerate(D['commands']) if x['stage']=='installed-client' and x['exit_code']==0]
assert len(prior_installed)==3
vsrc=git('scripts/validate-production-package-clients.py',e['head']);dsrc=git('tests/native_stable_clients_drill.py',e['head']);csrc=git('tests/native_stable_candidate_drill.py',e['head'])
for name,b in [('validate-production-package-clients.py.source',vsrc),('native_stable_clients_drill.py.source',dsrc),('native_stable_candidate_drill.py.source',csrc)]: (P/name).write_bytes(b)
def fn(raw,name):return next(n for n in ast.parse(raw).body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name==name)
def same_fn(path,name):return ast.dump(fn(git(path,e['base']),name),include_attributes=False)==ast.dump(fn(git(path,e['head']),name),include_attributes=False)
assert vsrc==git('scripts/validate-production-package-clients.py',e['base'])
assert same_fn('tests/native_stable_clients_drill.py','exercise') and same_fn('tests/native_stable_clients_drill.py','build_product')
base_client=fn(git('tests/native_stable_candidate_drill.py',e['base']),'export_client_generations');new_client=fn(csrc,'export_client_generations')
# The old body prefix remains exact until the new return into the shared exporter.
assert isinstance(new_client.body[-1],ast.Return)
assert ast.dump(ast.Module(body=new_client.body[:-1],type_ignores=[]),include_attributes=False)==ast.dump(ast.Module(body=base_client.body[:len(new_client.body)-1],type_ignores=[]),include_attributes=False)
shared=fn(csrc,'_export_generations');assert {x.arg:ast.literal_eval(v) for x,v in zip(shared.args.kwonlyargs,shared.args.kw_defaults) if v is not None}['include_preview'] is False
seal=fn(dsrc,'seal_generations');assert seal.args.kwonlyargs[0].arg=='preview' and ast.literal_eval(seal.args.kw_defaults[0]) is None
sign=fn(csrc,'sign_family');assert sign.args.kwonlyargs[0].arg=='channel' and ast.literal_eval(sign.args.kw_defaults[0])=='stable'
(patch:=P/'old-client-helper-delta.patch').write_bytes(subprocess.check_output(['git','diff',e['base'],e['head'],'--','tests/native_stable_candidate_drill.py','tests/native_stable_clients_drill.py'],cwd=ROOT))
# Generate the complete existing case inventory from the unchanged source topology.
assigns={t.id:n.value for n in ast.parse(vsrc).body if isinstance(n,ast.Assign) for t in n.targets if isinstance(t,ast.Name)}
clients=[('apt',ast.literal_eval(assigns['APT_CLIENTS'])),('rpm',ast.literal_eval(assigns['RPM_CLIENTS']))]
names=['baseline-'+g+'-four-download-and-installed-cli' for g in ['A','B']]
for family,items in clients:
 for dist,image in items:
  names += [dist+'-'+x for x in ['A-to-B-request-barrier','cache-A-first','cache-A-repeat','cache-B-target']]
  modes=['missing-byhash','corrupt-byhash'] if family=='apt' else ['missing-metadata','wrong-asc','wrong-mirror']
  names += [dist+'-reject-'+x for x in modes+['redirect','disconnect','untrusted-TLS','wrong-SAN']]
names += ['conditional-304-and-changed-generation','reject-exact-B-target-with-A-entrypoint','reject-actual-installed-source-SHA','reject-actual-installed-old-version','reject-actual-installed-missing-wkcli']
assert len(names)==49 and names[0]==D['cases'][0]['case']
put('coverage.json',{'planned':49,'recorded':1,'passed':0,'failed':1,'skipped_recorded':0,'not_reached':48,'recorded_cases':D['cases'],'not_reached_cases':names[1:],'installed_identity_receipts_available':0,'successful_installed_subcommands':prior_installed,'cache_scenarios_reached':0,'request_barriers_reached':0,'no_clients_A_B_artifacts':all(not(P/'artifacts/native-stable-four-clients-test-only'/('clients-'+g+'.json')).exists() for g in ['A','B'])})
put('first-failure.json',{'run_id':e['native_run'],'run_attempt':1,'job_id':119432670192,'head':e['head'],'actual_checkout':a['sha'],'tree':e['tree'],'receipt':facts(P/'artifacts/native-stable-four-clients-test-only/native-stable-clients.json'),'phase':D['phase'],'failure':D['failure'],'counts':{'expected':49,'recorded':1,'passed':0,'failed':1,'not_reached':48},'first_failed_command_index':index,'first_failed_command':c,'container_creation':create,'shell_execution_order':sequence,'timeout':{'container_seconds':600,'kill_after_seconds':10,'driver_seconds':650,'driver_timeout_fired':False,'interpretation':'Exit 124 at 600.151 seconds is consistent with the explicit inner timeout; exact blocked operation is not recorded.'},'boundaries':{'candidate_sha_and_signature_verified':True,'Alma_download_only_previous_command_exit':D['commands'][64]['exit_code'],'last_visible_external_repository_line':raw.decode().splitlines()[-1],'dnf_dependencies_resolved_observed':False,'dnf_transaction_completion_observed':False,'host_installed_cli_receipt_validation_reached':False,'container_python_acceptance_started':'unknown; stdout redirected, per-client file absent from artifact','selected_external_mirror_url':'unknown','dnf_internal_phase_at_timeout':'unknown','dns_network_resource_metrics':'not captured'},'requests':{'count':33,'serial':2,'all_status_200':True,'all_response_write_completed':True,'external_Alma_requests_in_fixture_log':False},'reader_cleanup':D['reader_cleanup'],'cleanup':D['cleanup'],'cache_cleanup':D['cache_cleanup']})
put('default-client-path-review.json',{'base':e['base'],'head':e['head'],'production_validator_byte_identical':True,'exercise_function_ast_identical':True,'build_product_function_ast_identical':True,'client_export_guard_prefix_ast_identical':True,'shared_export_include_preview_default':False,'seal_preview_default':None,'sign_family_default_channel':'stable','manual_semantic_checks':['Original exercise still calls export_client_generations with fixed product binaries and calls seal_generations without preview override.','Both binary versions are validated before sharing; payloads.get(version) accesses an already-populated A/B key on this path.','Default include_preview=False creates/exports only A/B and skips both new preview helpers; local evidence=None retains the old private evidence dictionary.','seal preview=None still constructs the original structural InputSnapshot at the same path.','Default stable sign_family paths/key filenames/arguments remain exactly the former stable constants.','Existing result gains data_only=False and source_provenance_verified=False; do not describe the entire helper/result schema as byte-identical.','New native M1 job is a separate isolated container/output, not a replacement for the 49-case workflow.'],'current_fixture':{k:D['fixture'][k] for k in ['data_only','source_sha','secret_tree_removed','source_provenance_verified']},'delta':facts(patch),'limits':'This source/default-path check and successful build/sign/download boundary are evidence against a new installer command change, not proof that the CI failure is exclusively infrastructure.'})
old_path=P.parent/'pr76-ci-generation-1/first-failure.json';old=json.loads(old_path.read_bytes());oc=old['first_failed_command'];ob=oc['output_tail'].encode();assert len(ob)==oc['output_size'] and sha(ob)==oc['output_sha256']
assert old['container_creation']['command'][-1]==script and old['container_creation']['command'][-7]==create['command'][-7]
old_native=P.parent/'pr76-ci-generation-1/artifacts/native-stable-four-clients-test-only/native-stable-clients.json';ov=json.loads(old_native.read_bytes())
assert sha(old_native.read_bytes())==old['receipt']['sha256']
binary_comparison={k:{'same_sha_size':all(D['product_build']['binaries'][k][f]==ov['product_build']['binaries'][k][f] for f in ['sha256','size']),'current':{f:D['product_build']['binaries'][k][f] for f in ['sha256','size']},'old':{f:ov['product_build']['binaries'][k][f] for f in ['sha256','size']}} for k in D['product_build']['binaries']}
put('prior-alma-failure-comparison.json',{'prior_first_failure_source':facts(old_path),'prior_native_receipt':facts(old_native),'prior_run_id':old['run_id'],'same_case':old['phase']==D['phase'],'same_pinned_image':create['command'][-7],'same_shell_bytes':True,'same_600_second_deadline':True,'prior_exit':oc['exit_code'],'current_exit':c['exit_code'],'prior_elapsed_seconds':oc['elapsed_seconds'],'current_elapsed_seconds':c['elapsed_seconds'],'prior_complete_output':oc['output_tail'],'current_complete_output':c['output_tail'],'both_complete_output_hashes_recomputed':True,'binary_sha_size_comparison':binary_comparison,'inference_limit':'Repeated boundary and slow public AppStream progress are consistent with external dependency delay. They do not identify the selected mirror or prove the exact later DNF wait/unique cause. Prior success on a different attempt does not pass this run.'})
legacy=[]
for name,n,nt in [('native-source-metadata',7,0),('native-local-signing',10,0),('native-bootstrap-preparation',6,0),('native-stable-candidate',31,8)]:
 v=R('artifacts/'+name+'-test-only/'+name+'.json');assert v['passed'] and len(v['cases'])==n and len(v.get('transport_cases',[]))==nt and all(x['passed'] for x in v['cases']+v.get('transport_cases',[]))
 if name=='native-stable-candidate':assert v['source_sha']==a['sha']
 for command in v.get('commands',[]):assert not command.get('timed_out',False)
 legacy.append({'name':name,'cases':n,'transport_cases':nt,'passed':True})
logs={n:(gzip.decompress((P/n).read_bytes()).decode() if n.endswith('.gz') else (P/n).read_text()) for n in ['pages-job.log','sdk-job.log','legacy-native-job.log.gz','stable-job.log']}
identities={n:re.findall(r'git log -1 --format=%H\n[^\n]*? ([0-9a-f]{40})\n',t) for n,t in logs.items()};assert all(x[0]==a['sha'] for x in identities.values())
assert identities['stable-job.log'][1:]==[R('trusted-toolchain-commit.json')['sha'],R('product-source-commit.json')['sha']]
put('actual-source-identity.json',{'head':e['head'],'head_tree':e['tree'],'packages_actual_checkout':a['sha'],'packages_actual_tree':a['tree']['sha'],'git_log_checkouts_by_job':identities,'git_objects_get_confirmed':True,'PR_current_merge_field_not_used_as_checkout_evidence':True,'product_source':{'sha':R('product-source-commit.json')['sha'],'tree':R('product-source-commit.json')['tree']['sha']},'trusted_toolchain_source':{'sha':R('trusted-toolchain-commit.json')['sha'],'tree':R('trusted-toolchain-commit.json')['tree']['sha']}})
artifact_proof=[]
for f in sorted(P.glob('artifacts/*/download-receipt.json')):
 v=json.loads(f.read_bytes());item=v['artifact'];assert v['whole_zip_verified_against_api_digest'] and v['all_members_crc_verified'] and item['workflow_run']['head_sha']==e['head']
 assert item['workflow_run']['id']==e['sdk_run' if item['name']=='object-storage-sdk-contract' else 'native_run']
 for m in v['members']:
  b=(f.parent/m['path']).read_bytes();assert len(b)==m['size'] and sha(b)==m['sha256'] and format(zlib.crc32(b)&0xffffffff,'08x')==m['crc32']
 artifact_proof.append({'name':item['name'],'id':item['id'],'size':v['zip_size'],'zip_sha256':v['zip_sha256_recomputed'],'API_digest_verified':True,'all_CRC_verified':True,'member_count':len(v['members'])})
assert len(artifact_proof)==7;put('artifact-identity-proof.json',artifact_proof)
jobs={label:R(label+'-jobs-final.json')['jobs'] for label in ['pages','sdk','native']}
put('job-step-outcomes.json',{label:[{'id':j['id'],'name':j['name'],'conclusion':j['conclusion'],'steps':[{'number':s['number'],'name':s['name'],'conclusion':s['conclusion']} for s in j['steps']]} for j in entries] for label,entries in jobs.items()})
summary={'head':e['head'],'base':e['base'],'tree':e['tree'],'actual_checkout':a['sha'],'pr_draft':pr['draft'],'all_runs_terminal':True,'attempt':1,'all_ci_passed':False,'pages':R('pages-verification.json'),'sdk':R('sdk-verification.json'),'legacy':legacy,'full_signed_repository_install_passed':True,'m1':{'cases':29,'passed':29,'scratch_removed':True,'secret_tree_removed':True,'ci_os_process_inventory':'not captured'},'stable':{'cases_recorded':1,'passed':0,'failed':1,'not_reached':48,'failure_phase':D['phase'],'installed_identity_receipts_available':0,'earlier_successful_installed_subcommands':3,'cache_scenarios_reached':0,'barrier_scenarios_reached':0,'reader_and_final_owned_inventory_empty':True,'cleanup_errors':[]},'stable_enabled':False,'public_release_or_origin_verified':False,'observer_reruns_dispatches_cancels':0,'observer_source_PR_cloud_mutations':0,'all_observer_processes_terminal':True}
put('summary.json',summary)
print(json.dumps({'terminal':True,'CI':'failed preserved','M1':29,'stable_passed':0,'stable_failed':1,'stable_not_reached':48,'artifacts_verified':7,'all_four_binary_sha_size_equal_prior':all(x['same_sha_size'] for x in binary_comparison.values())}))
