from pathlib import Path
import sys,json,os,tempfile,shutil,hashlib,time
sys.path.insert(0,'/source/tests');sys.path.insert(0,'/source')
import native_stable_candidate_drill as candidate
import native_stable_clients_drill as clients
import native_object_delivery_fixture_drill as fixture
out=Path('/output');public=Path('/public');exports=json.loads((public/'fixture-export.json').read_bytes())
evidence={'source_sha':'19ce29fb2233b3039e29c503963e27bd1fab972a','commands':[],'passed':False,'phase':'start','test_only':True,'started_at_unix':time.time(),'private_keys_generated_or_read':False}
scratch_path=None
try:
 assert sys.platform.startswith('linux') and os.geteuid()!=0
 assert exports['secret_tree_removed'] and exports['data_only'] and not exports['source_provenance_verified']
 with tempfile.TemporaryDirectory(prefix='wk-m2a-') as temporary:
  scratch=Path(temporary);scratch_path=scratch
  preview=fixture.Preview(public/'fixtures',exports['preview']);d=preview.descriptor();control=Path(d['control_dir'])
  archive=clients.module('scripts/archive-package-snapshot.py');extracted=scratch/'preview-extracted'
  archive.extract_snapshot(archive_path=preview.archive,output_dir=extracted,max_members=archive.DEFAULT_MAX_MEMBERS,max_total_size=1024*1024*1024)
  evidence['phase']='verify-native-preview'
  cmd=[sys.executable,'-B','/source/scripts/verify-production-package-site.py','--site-root',str(extracted/'site'),'--snapshot',str(extracted/'audit/snapshot.json'),'--channels',str(control/'manifests/channels.json'),'--signing',str(control/'manifests/preview-signing.json'),'--apt-public-cert',str(control/'keys/apt-preview.asc'),'--rpm-public-cert',str(control/'keys/rpm-preview.asc')]
  verified=clients.run_command(evidence,evidence['phase'],cmd,timeout=180);assert verified.returncode==0
  evidence['phase']='seal-plan-native-A-B'
  descriptors,plans,inputs,used=clients.seal_generations(exports,public/'fixtures',scratch,evidence,preview=preview);assert used is preview
  resource=scratch/'resource.json';resource.write_bytes(candidate.canonical(fixture.RESOURCE));shutil.copyfile(resource,out/'resource.json')
  (out/'descriptors.json').write_bytes(candidate.canonical(descriptors))
  evidence['generations']={}
  for label in ('A','B'):
   target=out/label;target.mkdir();prepared=scratch/('delivery-'+label)
   evidence['phase']='prepare-'+label
   plan_sha=hashlib.sha256(candidate.canonical(plans[label])).hexdigest()
   cmd=[sys.executable,'-B','/source/scripts/prepare-object-storage-delivery.py','--inputs',str(inputs[label]),'--expected-plan-sha256',plan_sha,'--output-dir',str(prepared),'--resource-intent',str(resource),'--max-routes','10000','--composite','--test-only']
   result=clients.run_command(evidence,evidence['phase'],cmd,timeout=180);assert result.returncode==0
   delivery=json.loads((prepared/'delivery.json').read_bytes());plan=plans[label]
   assert len(delivery['objects'])==153 and len(delivery['routes'])==178
   assert delivery['planner_verification']==plan['verification']
   assert all(delivery[k] is False for k in ('production_enabled','publication_verified','cloud_verified'))
   expected={x['object_key']:x for n in ('snapshot_objects','immutable_aliases','generated_objects') for x in plan[n]}
   assert {x['object_key'] for x in delivery['objects']}==set(expected)
   for item in delivery['objects']:
    assert candidate.facts(prepared/item['materialized_file'])=={k:item[k] for k in ('sha256','size')}
    assert all(item[k]==v for k,v in expected[item['object_key']].items())
   for name,origin in [('delivery.json',prepared/'delivery.json'),('transport-plan.json',prepared/'transport-plan.json'),('inputs.json',inputs[label]),('resolution.json',scratch/('resolution-'+label+'.json'))]:shutil.copyfile(origin,target/name)
   (target/'routes.json').write_bytes(candidate.canonical(delivery['routes']))
   evidence['generations'][label]={'objects':153,'routes':178,'plan':candidate.facts(target/'transport-plan.json'),'delivery':candidate.facts(target/'delivery.json'),'routes_facts':candidate.facts(target/'routes.json'),'all_object_bytes_and_bindings_verified':True,'client_audit_ids':[x['audit_release_id'] for x in delivery['client_requests']]}
  evidence['verification']={'native_preview_verified':True,'native_stable_verified':True,'github_provenance_verified':False,'fresh_production_auth_verified':False,'sdk_integration_verified':False,'cloud_verified':False,'public_clients_verified':False,'activation_authorized':False}
  evidence['phase']='complete';evidence['passed']=True
except Exception as error:
 evidence['error_type']=type(error).__name__;evidence['error']=str(error)[:2000]
finally:
 evidence['scratch_removed']=scratch_path is None or not scratch_path.exists();evidence['finished_at_unix']=time.time()
 (out/'result.json').write_bytes(candidate.canonical(evidence))
print(json.dumps({'passed':evidence['passed'],'phase':evidence['phase'],'error':evidence.get('error'),'scratch_removed':evidence['scratch_removed']}))
raise SystemExit(0 if evidence['passed'] and evidence['scratch_removed'] else 1)
