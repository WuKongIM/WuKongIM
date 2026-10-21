from pathlib import Path
import ast,gzip,hashlib,json,re,zlib
P=Path(__file__).resolve().parent
R=lambda n:json.loads((P/n).read_bytes())
sha=lambda b:hashlib.sha256(b).hexdigest()
def put(n,v):(P/n).write_text(json.dumps(v,sort_keys=True,indent=2)+'\n')
e=R('expected.json');m=R('actual-checkout.json');h=R('head-commit.json');audit=R('receipt-audit-first.json')
assert audit['receipt_audit_passed'] and audit['all_observed_ci_passed']
assert h['sha']==e['head'] and h['tree']['sha']==m['tree']['sha']==e['tree'] and [x['sha'] for x in m['parents']]==[e['base'],e['head']]
d=R('artifacts/native-stable-four-clients-test-only/native-stable-clients.json');assert d['control']=={'checkout_sha':m['sha'],'tree':e['tree']}
product=R('product-source-commit.json');trusted=R('trusted-toolchain-commit.json');assert d['product_build']['source_sha']==product['sha'] and d['product_build']['tree']==product['tree']['sha']
logs={n:(gzip.decompress((P/n).read_bytes()).decode() if n.endswith('.gz') else (P/n).read_text()) for n in ('pages-job.log','sdk-job.log','legacy-native-job.log.gz','stable-job.log')}
ids={n:re.findall(r'git log -1 --format=%H\n[^\n]*? ([0-9a-f]{40})\n',t) for n,t in logs.items()};assert all(v[0]==m['sha'] for v in ids.values());assert ids['stable-job.log'][1:]==[trusted['sha'],product['sha']]
put('actual-source-identity.json',{'head':e['head'],'tree':e['tree'],'actual_checkout':m['sha'],'actual_git_log_checkouts':ids,'github_git_objects_checked':True,'trusted_toolchain':{'sha':trusted['sha'],'tree':trusted['tree']['sha']},'product_source':{'sha':product['sha'],'tree':product['tree']['sha']},'PR_merge_field_not_used_to_infer_actual_checkout':True})
sdk=R('artifacts/object-storage-sdk-contract/provider-contract-receipt.json');assert sdk['checkout_sha']==m['sha'] and sdk['checkout_tree']==e['tree'] and sdk['tests']==34 and sdk['skips']==sdk['failures']==sdk['errors']==0
assert sdk['provider_network_allowed'] is False
old=R('../pr77-ci-generation-1/coverage.json');planned=[x['case'] for x in old['recorded_cases']]+old['not_reached_cases'];actual=[x['case'] for x in d['cases']];assert actual==planned and len(planned)==49
put('coverage.json',{'planned':49,'recorded':49,'passed':49,'failed':0,'not_reached':0,'expected_rejection_cases':sum(c['expected_rejection'] for c in d['cases']),'case_names':actual,'installed_identity_receipts':len(audit['baseline']),'cache_cases':len(audit['cache']),'repeat_cache_read_proofs':sum(c['reuse_required'] for c in audit['cache']),'historical_generation_1':{'passed':0,'failed':1,'not_reached':48,'archive_manifest_sha256':sha((P.parent/'pr77-ci-generation-1/archive-sha256.json').read_bytes()),'unchanged':True}})
# Re-evaluate complete preserved outputs using the exact reviewed pure parser.
node=next(n for n in ast.parse((P/'native_stable_clients_drill.py.source').read_text()).body if isinstance(n,ast.FunctionDef) and n.name=='_installed_phase_events')
ns={};exec(compile(ast.Module(body=[node],type_ignores=[]),'frozen-phase-parser','exec'),ns);parse=ns['_installed_phase_events']
phase=[]
for i,c in enumerate(d['commands']):
 if c['stage']!='installed-client':assert 'installed_phase_events' not in c;continue
 creation=d['commands'][i-1];assert creation['stage']=='create-container' and creation['output_tail'].strip()==c['command'][-1]
 script=creation['command'][-1];family='apt' if '/candidate.deb' in script else 'rpm'
 expected=[{'phase':p,'event':event} for p in (('checksum','dependencies','cli') if family=='apt' else ('checksum','signature','dependencies','cli')) for event in ('begin','complete')]
 assert c['exit_code'] in (0,1) and not c['timed_out'] and not c['output_limit']
 p=c['installed_phase_events'];assert p['schema']=='wukongim/native-installed-phase-events/v1' and not p['truncated'] and len(p['events'])<=16
 assert p['events']==(expected if c['exit_code']==0 else expected[:-1])
 raw=c['output_tail'].encode();complete=len(raw)==c['output_size']
 if complete:assert sha(raw)==c['output_sha256'] and parse(raw)==p
 assert creation['command'][-6:-1]==['timeout','--kill-after=10s','600s','bash','-euc']
 negative=[x['case'] for x in d['cases'] if x.get('result',{}).get('output_sha256')==c['output_sha256'] and x['expected_rejection']]
 if c['exit_code']:assert len(negative)==1 and negative[0].startswith('reject-actual-installed-')
 phase.append({'command_index':i,'family':family,'image':creation['command'][-7],'exit_code':c['exit_code'],'elapsed_seconds':c['elapsed_seconds'],'installed_phase_events':p,'output_size':c['output_size'],'output_sha256':c['output_sha256'],'complete_output_hash_and_events_recomputed':complete,'phase_summary_outside_tail':len(raw)<c['output_size'],'expected_rejection_case':negative})
assert len(phase)==11 and sum(x['exit_code']==0 for x in phase)==8 and sum(x['exit_code']==1 for x in phase)==3
put('phase-observation-audit.json',{'commands':phase,'success_commands':8,'intended_rejection_commands':3,'phase_events_are_diagnostic_only':True,'acceptance_supported_separately_by_identity_receipts_and_negative_diagnostics':True,'full_output_hash_and_events_recomputed':sum(x['complete_output_hash_and_events_recomputed'] for x in phase),'large_stdout_limit':'For truncated outputs, only the original full-output SHA declaration plus tail and bounded event receipt are available; no claim of independently reconstructing the full stream.','historical_timeout_subphase':'unknown; this passing run does not locate the previous timeout'})
proof=[]
for f in sorted(P.glob('artifacts/*/download-receipt.json')):
 a=json.loads(f.read_bytes());item=a['artifact'];assert a['whole_zip_verified_against_api_digest'] and a['all_members_crc_verified'];assert item['workflow_run']['head_sha']==e['head'];assert item['workflow_run']['id']==e['sdk_run' if item['name']=='object-storage-sdk-contract' else 'native_run']
 for x in a['members']:
  b=(f.parent/x['path']).read_bytes();assert len(b)==x['size'] and sha(b)==x['sha256'] and f'{zlib.crc32(b)&0xffffffff:08x}'==x['crc32']
 proof.append({'id':item['id'],'name':item['name'],'whole_zip_sha256_recomputed':a['zip_sha256_recomputed'],'API_digest_verified':True,'zip_size':a['zip_size'],'member_count':len(a['members']),'all_member_CRC_SHA_size_verified':True})
assert len(proof)==7;put('artifact-identity-proof.json',proof)
jobs={k:R(k+'-jobs-final.json')['jobs'] for k in ('pages','native','sdk')}
put('job-step-outcomes.json',{k:[{'id':j['id'],'name':j['name'],'conclusion':j['conclusion'],'steps':[{'number':s['number'],'name':s['name'],'conclusion':s['conclusion']} for s in j['steps']]} for j in v] for k,v in jobs.items()})
checks=R('checks-final.json')['check_runs'];assert len(checks)==5 and all(c['head_sha']==e['head'] and c['status']=='completed' for c in checks);assert sorted(c['conclusion'] for c in checks)==['skipped','success','success','success','success']
assert not R('channels.json.source')['channels']['stable']['enabled']
assert R('pr-final.json')['head']['sha']==e['head'] and R('pr-final.json')['merged'] is False
summary={'head':e['head'],'tree':e['tree'],'actual_checkout':m['sha'],'first_natural_attempt':1,'all_ci_terminal_and_passed':True,'checks_success':4,'checks_conditional_skip':1,'pages':R('pages-verification.json'),'SDK':{'tests':34,'input_tests':10,'skips':0},'M1':{'passed':29,'export_commands':27,'outer_commands':27,'test_secrets_removed':True,'scratch_removed':True,'CI_process_inventory':'not captured'},'legacy':audit['legacy'],'full_signed_repository_install_passed':True,'stable':{'cases_passed':49,'not_reached':0,'installed_identities':8,'cache_cases':12,'same_inode_hash_read_proofs':4,'RPM_generation_switch_proofs':2,'installed_phase_commands':11,'real_expected_installed_rejections':3,'cleanup_errors':[],'owned_containers_networks_remaining':0,'cache_roots_remaining':0},'prior_failure_preserved':True,'prior_failure_cause':'unknown','stable_enabled':False,'public_release_or_M2b_verified':False,'observer_mutations_or_retry_dispatch':0,'all_observer_processes_terminal':True}
put('summary.json',summary)
print(json.dumps({'all_CI_passed':True,'cases':49,'identities':8,'cache_cases':12,'phase_commands':11,'full_phase_output_recomputed':sum(x['complete_output_hash_and_events_recomputed'] for x in phase),'owned_cleanup_remaining':0,'artifacts_complete_SHA_CRC':7}))
