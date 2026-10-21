#!/usr/bin/env python3
import collections,datetime,hashlib,json
from pathlib import Path
D=Path(__file__).parent
req='chat-20261021T163850Z-4ccd7506';sha='cee5d51f325fb84033060f546337d326110adae5'
q=json.loads((D/'quote/verified/quote.json').read_text())['quote']; a=json.loads((D/'acquire/verified/receipt.json').read_text())['receipt']; p=json.loads((D/'quote/observed-plan.json').read_text());b=json.loads((D/'bundle/verified/verification.json').read_text())
assert (D/'quote/verified/quote.json').read_bytes()==(D/'acquire/verified/quote.json').read_bytes()
assert a['quote']==q and a['state']=='active' and a['request_id']==q['request_id']==p['request_id']==req
assert a['lease_id']==q['lease_id']==p['lease_id']==req+'-rehearsal-1'
assert a['provider']==q['provider']==p['provider']=='alibaba' and a['region']==q['region']==p['region']=='cn-hangzhou'
assert a['zone']==q['zone']=='cn-hangzhou-b' and a['repository']==p['repository']=='WuKongIM/WuKongIM'
assert a['operator']==p['operator']=='tangtaoit'
assert a['budget']==p['budget']=={'currency':'CNY','limit_micros':1351044800,'committed_micros':0,'operational_stop_micros':1201044800}
assert a['plan_digest']==q['plan_digest']
assert a['provenance']==p['provenance']=={'source_sha':sha,'bundle_digest':b['bundle_digest']}
assert q['estimated_cost_micros']==74477600 and q['capacity_available'] is True and q['quota_available'] is True
assert a['expires_at']==p['expires_at']
required={'wukongim-request-id':req,'wukongim-lease-id':a['lease_id'],'wukongim-lease-plan-digest':a['plan_digest'],'wukongim-source-sha':sha,'wukongim-bundle-digest':b['bundle_digest'],'wukongim-repository':a['repository'],'wukongim-provider':'alibaba','wukongim-region':'cn-hangzhou','wukongim-expires-at':a['expires_at'],'wukongim-managed-by':'wukongim-cloud-lease','stage':'rehearsal'}
for key,value in required.items():assert a['tags'][key]==value
expected={'disk':8,'disk-attachment':4,'eip':1,'eip-association':1,'eni':4,'instance':4,'security-group':1,'security-rule':3,'vpc':1,'vswitch':1}
assert dict(collections.Counter(x['kind'] for x in a['resources']))==expected
assert len({(x['kind'],x['id']) for x in a['resources']})==len(a['resources'])
for x in a['resources']:
 for key,value in required.items():assert x['tags'][key]==value,(x['kind'],key)
 assert x['tags']['wukongim-alibaba-account-hash']==a['account_id_hash']
 assert x['tags']['wukongim-alibaba-quote-cost-micros']=='74477600'
 assert x['tags']['wukongim-alibaba-lifecycle']=='active'
instances=[x for x in a['resources'] if x['kind']=='instance'];assert dict(collections.Counter(x['role'] for x in instances))=={'service':3,'load':1}
assert all(x['attributes']['instance_type']==q['selection']['instance_type']=='ecs.u2a-c1m2.xlarge' for x in instances)
assert [(x['role'],x['count']) for x in p['host_groups']]==[('service',3),('load',1)]
assert all(x['compute']['vcpus']==4 and x['compute']['memory_bytes']==8589934592 for x in p['host_groups'])
assert len(a['access_grants'])==2 and {x['port_from'] for x in a['access_grants']}=={22,80}
for x in a['access_grants']:assert x['target_role']=='load' and x['protocol']=='tcp' and x['port_from']==x['port_to'] and x['source_prefix']=='0.0.0.0/0' and x['until']==a['expires_at']
now=datetime.datetime.now(datetime.timezone.utc).isoformat()
summary={'observed_at':now,'identity_and_budget_checks':'passed','source_sha':sha,'control_sha':sha,'request_id':req,'lease_id':a['lease_id'],'state':a['state'],'region':a['region'],'zone':a['zone'],'created_at':a['created_at'],'expires_at':a['expires_at'],'bundle_run_id':39637578380,'quote_run_id':39637891019,'acquire_run_id':39639010616,'top_run_id':39637426411,'all_attempts':1,'bundle_digest':b['bundle_digest'],'plan_digest':a['plan_digest'],'budget':a['budget'],'new_quote_cost_micros':q['estimated_cost_micros'],'prior_task_quote_reserve_micros':148955200,'prior_task_reserve_plus_new_full_quote_micros':223432800,'actual_provider_bill':None,'old_reserve_is_not_new_request_prior':True,'resource_counts':expected,'instance_role_counts':{'service':3,'load':1},'all_resource_identity_tags_match':True,'temporary_access_ports':[22,80],'access_until':a['expires_at'],'quote_bytes_same_in_quote_and_acquire':True,'receipt_selector_identity_fields_match':True,'actual_top_release_selector_observed':False,'measured_run_start_observed':False,'limits':['Authenticated exact-source workflow receipts checked; no independent provider API read','Acquired resources do not imply deployed application health or measured workload start','Actual top-level release-selector.json is not yet published; only receipt identity and every resource tag are checked here','Receipt committed_micros=0 is the admitted new-request prior, not a statement of zero provider bill','Full Quote reserve comparison is conservative planning accounting, not an invoice','Run-title correlation and exact source are verified; later handoff must confirm its explicit bundle/acquire/deployment producer IDs']}
(D/'acquire-verification.json').write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps(summary,indent=2))
