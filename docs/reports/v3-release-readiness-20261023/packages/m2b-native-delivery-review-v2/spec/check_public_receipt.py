from pathlib import Path
import collections,hashlib,json
R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-delivery-sdk')
E=Path('/tmp/wukongim-v3-readiness-20261021/packages/m2b-native-delivery-sdk-repair-v2')
O=Path(__file__).parent
p=E/'native-validation/native-sdk.json';raw=p.read_bytes();x=json.loads(raw)
fpath=R/'tests/provider_sdk/fixtures/native-composite-routes.json';fixture=json.loads(fpath.read_bytes())
ex=collections.defaultdict(list)
for row in x['sdk_exchange_observations']:ex[row['wire']].append(row)
ops=x['operation_receipts'];checks=[]
for label in ('A','B'):
 actual=[r for r in ops if r['wire']==0 and r['kind']=='object' and r['generation']==label]
 expected=[{k:v for k,v in ob.items() if k not in ('materialized_file','content_utf8')} for ob in fixture['generations'][label]['delivery.json']['objects']]
 assert [r['object'] for r in actual]==expected and len(actual)==153
 for op in actual:
  assert op['state']=='complete'
  facts={k:op['object'][k] for k in ('size','sha256','cache_control')}
  assert {k:op['result']['readback'][k] for k in facts}==facts
  pair=ex[op['wire']][op['first_exchange']:op['end_exchange']]
  assert len(pair)==2 and [q['method'] for q in pair]==['PUT','GET']
  put,get=pair
  assert put['object_key']==get['object_key']==op['object']['object_key']
  if put['status']==200:
   assert put['request_body']['sha256']==facts['sha256'] and put['request_body']['bytes_read']==facts['size'] and put['request_body']['eof']
  else:
   assert put['status']==409 and op['result']['outcome']=='already_exists' and put['request_body']['bytes_read']==0 and put['request_body']['eof'] is False
  assert put['request']['forbid_overwrite']=='true' and put['request']['cache_control']==facts['cache_control']
  assert get['status']==200 and get['stream']['eof'] and get['stream']['sha256']==facts['sha256'] and get['stream']['bytes_read']==facts['size']
  assert get['response']['cache_control']==facts['cache_control'] and int(get['response']['content_length'])==facts['size']
 checks.append({'generation':label,'objects':len(actual),'ordered_exact_native_manifest_match':True,'actual_create_only_and_response_accurate':True,'actual_full_get_eof_sha_size_cache_match':True})
configchecks=[]
for op in [r for r in ops if r['kind']=='config']:
 exchange=ex[op['wire']][op['first_exchange']:op['end_exchange']]
 observed=[r['response']['DomainConfigs']['DomainConfig'] for r in ex[op['wire']] if r['action']=='DescribeCdnDomainConfigs' and 'response' in r]
 assert op['before']['configs'] in observed or op['state']=='incomplete'
 if 'after' in op:assert op['after']['configs'] in observed
 configchecks.append({'generation':op['generation'],'wire':op['wire'],'state':op['state'],'before_and_after_present':'before' in op and 'after' in op,'after_bound_to_actual_sdk_response':'after' in op})
refreshchecks=[]
for op in [r for r in ops if r['kind']=='refresh']:
 exchange=ex[op['wire']][op['first_exchange']:op['end_exchange']]
 submits=[r for r in exchange if r['action']=='RefreshObjectCaches']
 query=[r for r in exchange if r['action']=='DescribeRefreshTaskById']
 expected=['https://packages.githubim.com'+r['public_uri'] for r in fixture['generations'][op['generation']]['delivery.json']['routes']]
 assert op['urls']==expected and len(expected)==178
 if op['state']=='complete':
  assert [u for r in submits for u in r['request']['ObjectPath']]==expected
  rows=[v for r in query for v in r['response']['Tasks']]
  assert [v['ObjectPath'] for v in rows]==expected and all(v['Status']=='Complete' for v in rows)
  assert all(r['response']['TotalCount']==len(r['response']['Tasks']) for r in query)
 refreshchecks.append({'wire':op['wire'],'state':op['state'],'exact_requested_scope':True,'submitted_url_count':sum(len(r['request']['ObjectPath']) for r in submits),'actual_response_rows':sum(len(r.get('response',{}).get('Tasks',[])) for r in query),'request_types':sorted(set(r['request']['ObjectType'] for r in submits))})
assert len([r for r in configchecks if r['state']=='complete'])==2
assert len([r for r in refreshchecks if r['state']=='complete'])==2
assert all(v is False for v in x['verification'].values())
assert x['network_guard']=={'self_check_attempts':1,'unexpected_attempts':0,'passed':True}
result={'receipt':str(p),'receipt_sha256':hashlib.sha256(raw).hexdigest(),'receipt_bytes':len(raw),'independent_manifest':str(fpath),'independent_manifest_sha256':hashlib.sha256(fpath.read_bytes()).hexdigest(),'object_checks':checks,'configuration_checks':configchecks,'refresh_checks':refreshchecks,'no_true_production_claims':True,'native_sdk_rerun':False,'actual_exchange_count':len(x['sdk_exchange_observations']),'operation_count':len(ops)}
(O/'public-receipt-verification.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'matched_native_objects':306,'configuration_operations':len(configchecks),'refresh_operations':len(refreshchecks),'result':'pass'}))
