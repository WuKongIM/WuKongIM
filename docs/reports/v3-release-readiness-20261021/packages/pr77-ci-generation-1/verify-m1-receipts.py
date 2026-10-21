from pathlib import Path
import ast,hashlib,json
P=Path(__file__).resolve().parent; R=lambda n:json.loads((P/n).read_bytes())
e=R('expected.json');source=ast.parse((P/'native_object_delivery_fixture_drill.py.source').read_text())
expected=next(ast.literal_eval(n.value) for n in source.body if isinstance(n,ast.Assign) and any(isinstance(x,ast.Name) and x.id=='CASES' for x in n.targets))
a=R('actual-checkout-e306351.json');h=R('head-commit.json')
assert h['sha']==e['head'] and h['tree']['sha']==a['tree']['sha']==e['tree']
assert [x['sha'] for x in a['parents']]==[e['base'],e['head']]
r=R('artifacts/native-object-delivery-fixture-test-only/result.json');f=R('artifacts/native-object-delivery-fixture-test-only/fixture-export.json')
assert r['source_sha']==a['sha'] and r['passed'] and r['failure'] is None and r['test_only'] and r['scratch_removed']
assert len(r['cases'])==len(expected)==29 and {x['case'] for x in r['cases']}==set(expected) and all(x['passed'] for x in r['cases'])
assert f['test_only'] and f['data_only'] and f['secret_tree_removed'] and f['source_sha'] is None and f['source_provenance_verified'] is False
assert set(f['generations'])=={'A','B'} and f['generations']['A']['version']=='3.0.0' and f['generations']['B']['version']=='3.0.1'
assert f['preview']['descriptor']['audit_release_id']==10
assert all(r['verification'][x] is True for x in ['data_only_packages','native_preview_verified','native_stable_verified'])
assert all(r['verification'][x] is False for x in ['installed_cli_verified','github_provenance_verified','fresh_production_auth_verified','sdk_integration_verified','cloud_verified','public_clients_verified','activation_authorized'])
assert len(r['export']['commands'])==27
for c in r['export']['commands']:assert c['exit_code']==0 and not c['timed_out']
assert [c['stage'] for c in r['export']['commands']][-4:]==['preview-verify-after-secret-removal','preview-archive','preview-audit-create','preview-audit-verify']
complete=0;negatives=[]
for c in r['commands']:
 assert not c['timed_out'] and not c['output_limit']
 assert c['exit_code']==(1 if c['stage'].startswith('reject-') else 0)
 raw=c['output_tail'].encode()
 if len(raw)==c['output_size']:
  assert hashlib.sha256(raw).hexdigest()==c['output_sha256'];complete+=1
 if c['exit_code']:negatives.append({'stage':c['stage'],'exit_code':c['exit_code'],'diagnostic':c['output_tail']})
assert len(r['commands'])==27
print(json.dumps({'passed':True,'actual_checkout':a['sha'],'actual_tree':a['tree']['sha'],'case_count':29,'case_names':[x['case'] for x in r['cases']],'export_command_count':27,'outer_command_count':27,'complete_outer_output_hashes_recomputed':complete,'negative_commands':negatives,'verification':r['verification'],'materializations':r['materializations'],'secret_tree_removed':f['secret_tree_removed'],'scratch_removed':r['scratch_removed'],'ci_os_process_inventory':'not captured; unknown','ci_zombie_process_count':'not captured; unknown','prior_local_pid_limit_failure_separate':True,'no_native_execution_by_observer':True},indent=2))
