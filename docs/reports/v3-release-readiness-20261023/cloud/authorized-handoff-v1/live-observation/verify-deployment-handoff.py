#!/usr/bin/env python3
"""Offline bounded public-receipt cross-check; never decrypts or contacts a provider."""
import base64,datetime,hashlib,json,re,subprocess,zipfile
from pathlib import Path
ROOT=Path('/tmp/wukongim-v3-readiness-20261021/cloud/pr1024-authorized-run-resume-20261023')
LIVE=ROOT/'live-observation'; PRIVATE=ROOT.parent/'pr1024-authorized-run-resume-20261023-private/live-observation'
OUT=LIVE/'handoff/verified'; OUT.mkdir()
SRC='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06'; REQUEST='chat-20261021T214505Z-00299e37'; LEASE=REQUEST+'-rehearsal-1'
sha=lambda b:hashlib.sha256(b).hexdigest()
def pairs(items):
 d={}
 for k,v in items: assert k not in d;d[k]=v
 return d
def parse(b):return json.loads(b,object_pairs_hook=pairs)
def read(p):return parse(p.read_bytes())
def save(n,v):
 p=OUT/n;p.parent.mkdir(parents=True,exist_ok=True)
 with p.open('x') as f:json.dump(v,f,ensure_ascii=False,indent=2);f.write('\n')
def prefixed(value):
 assert re.fullmatch(r'(sha256:)?[0-9a-f]{64}',value)
 return value if value.startswith('sha256:') else 'sha256:'+value
checks=[]
def check(name,v):
 assert v,name
 checks.append(name)
meta=read(LIVE/'handoff/handoff-metadata.json'); raw=(PRIVATE/'handoff-12446490247.zip').read_bytes()
check('handoff complete ZIP actual bytes and SHA equal GitHub metadata',len(raw)==meta['size_in_bytes']<1024*1024 and 'sha256:'+sha(raw)==meta['digest'])
check('handoff producer exact main source/run',meta['workflow_run']['id']==39820950900 and meta['workflow_run']['head_sha']==SRC and meta['workflow_run']['head_branch']=='main')
selected={'analysis-endpoint.json','deployment-outcome.json','deployment-plan.json','handoff.json','issue-preflight.json','release-selector.json','request-budget.json','run-plan.json','run-start.json','continuation-authentication.json','attempt-1/pre-clock-generation-1/unit.json','attempt-1/pre-clock-generation-1/start-stderr.json','attempt-1/pre-clock-generation-1/start-stdout.json','attempt-1/pre-clock-generation-1/state-stderr.json','attempt-1/pre-clock-generation-1/state-stdout.json'}
records=[]; payload={}; data={}
with zipfile.ZipFile(PRIVATE/'handoff-12446490247.zip') as z:
 check('handoff ZIP CRC and unique bounded member names',z.testzip() is None and len(z.namelist())==len(set(z.namelist()))<=100)
 for info in z.infolist():
  check('safe member '+info.filename,not info.is_dir() and not info.filename.startswith('/') and '..' not in Path(info.filename).parts and info.file_size<1024*1024)
  b=z.read(info.filename);records.append({'member':info.filename,'bytes':len(b),'sha256':sha(b),'selected_public':info.filename in selected})
  payload[info.filename]=b
  if info.filename.endswith('.json'):data[info.filename]=parse(b)
 check('exact selected public members exist', selected <= set(payload))
 for name in selected:
  b=payload[name];check('no private-key header '+name,b'-----BEGIN' not in b)
  p=OUT/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(b)
for name in ['receipt.json','quote.json']:
 check('acquire original exact bytes '+name,payload[name]==(LIVE/'acquire/verified'/name).read_bytes())
for name in ['analysis-endpoint.json','deployment-outcome.json','deployment-plan.json']:
 check('deployment original exact bytes '+name,payload[name]==(LIVE/'deployment/verified'/name).read_bytes())
r=read(LIVE/'acquire/verified/receipt.json')['receipt']; dp=data['deployment-plan.json']; do=data['deployment-outcome.json']; dr=do['receipt']; ready=read(LIVE/'deployment/verified/readiness-snapshot.json'); h=data['handoff.json']; start=data['run-start.json']; plan=data['run-plan.json']; sel=data['release-selector.json']['selector']; ep=data['analysis-endpoint.json']
check('Acquire→deployment digest uses official deploymentDigest normalization',dp['lease_plan_digest']==prefixed(r['plan_digest'])==dr['lease_plan_digest'])
check('all request/Lease identities',all(x['lease_id']==LEASE and x['request_id']==REQUEST for x in [r,dp,dr,sel,ep]))
check('all product/control SHA and bundle identities',all(x['source_sha']==x['control_sha']==x['lease_source_sha']==SRC and x['bundle_digest']==x['lease_bundle_digest']==r['provenance']['bundle_digest'] for x in [dp,dr]))
check('repository provider region',all(x['repository']=='WuKongIM/WuKongIM' and x['provider']=='alibaba' and x['region']=='cn-hangzhou' for x in [r,dp,sel]))
check('deployment immutable generation1 and success',do['passed'] is True and dp['purpose']==dr['purpose']=='immutable' and dp['generation']==dr['generation']==1)
check('deployment plan digest all receipt bindings',dp['plan_digest']==dr['deployment_plan_digest']==ready['deployment_plan_digest']==ep['deployment_plan_digest'])
# Match Go encoding/json with the original struct field order; none of these values need HTML escaping.
unsigned=dict(dp);unsigned['plan_digest']=''
check('deployment plan digest recomputed',dp['plan_digest']=='sha256:'+sha(json.dumps(unsigned,separators=(',',':'),ensure_ascii=False).encode()))
check('expiry and activation binding',dp['expires_at']==dr['lease_expires_at']==ep['lease_expires_at']==r['expires_at'] and dp['lease_created_at']==r['created_at'] and dr['activated_at']==ready['observed_at'])
budget={'currency':'CNY','limit_micros':1276567200,'committed_micros':0,'operational_stop_micros':1126567200}
check('exact new-request budget no carried prior commitment',r['budget']==plan['lease_plan']['budget']==budget and all(dp['budget'][k]==v for k,v in budget.items()) and data['request-budget.json']=={k:budget[k] for k in ['limit_micros','operational_stop_micros']})
check('original Quote estimate and items preserved',dp['budget']['estimated_cost_micros']==r['quote']['estimated_cost_micros']==74477600 and dp['budget']['line_items']==r['quote']['line_items'])
resources={x['id']:x for x in r['resources']}; rh={x['role']:x for x in ready['hosts']}; oh={x['role']:x for x in dr['hosts']}
check('four exact distinct host roles',set(rh)==set(oh)=={'service-1','service-2','service-3','load'} and len(dp['hosts'])==4)
for host in dp['hosts']:
 role=host['role']; ins=resources[host['instance_id']];disk=resources[host['data_disk_id']]
 check('Acquire resources→deployment host '+role,ins['kind']=='instance' and ins['role']==host['lease_role'] and ins['private_address']==host['private_address'] and disk['kind']=='disk' and disk['parent_id']==ins['id'] and disk['attributes']['disk_type']=='data')
 check('activation/readiness exact host '+role,all(oh[role][k]==host[k] for k in ['instance_id','private_address','data_disk_id']) and rh[role]['data_disk_id']==host['data_disk_id'] and rh[role]['bundle_digest']==oh[role]['bundle_digest']==dp['bundle_digest'] and rh[role]['data_filesystem_bytes']>=host['minimum_data_filesystem_bytes'])
check('3 service nodes 256 healthy slots 0 pending',all(ready['cluster'][k]==v for k,v in {'ready_nodes':3,'members':3,'physical_hash_slots':256,'healthy_slot_leaders':256,'healthy_slot_replica_sets':256,'pending_controller_tasks':0}.items()))
check('3 workers and 7/7 targets',all(ready['load'][k]==v for k,v in {'ready_workers':3,'prometheus_targets_up':7,'prometheus_targets_want':7,'workload_config_valid':True,'analysis_ready':True}.items()))
check('release selector exact Acquire identity',all(sel[k]==r[k] for k in ['lease_id','request_id','provider','region','repository','plan_digest']))
check('handoff exact parent-chain',h['request_id']==REQUEST and h['attempt']==1 and h['source_sha']==SRC and h['bundle_digest']==dp['bundle_digest'] and h['bundle_run_id']==39821046654 and h['acquire_run_id']==39822336878 and h['deployment_run_id']==39822605006)
check('first-start not continuation',data['continuation-authentication.json']['status']=='first_start' and data['continuation-authentication.json']['attempt']==1 and data['continuation-authentication.json']['committed_micros']==0)
check('run-start exact rehearsal1 and Lease run hash',start['schema']=='wukongim.chat_lifecycle.run_start/v1' and start['stage']=='rehearsal' and start['generation']==1 and start['run_hash']=='sha256:'+sha(LEASE.encode()))
a=datetime.datetime.fromisoformat(start['started_at']);b=datetime.datetime.fromisoformat(start['expected_end_at'])
check('real measured window exactly2h after activation',b-a==datetime.timedelta(hours=2) and a>datetime.datetime.fromisoformat(dr['activated_at'].replace('Z','+00:00')) and h['started_at']==start['started_at'] and h['expected_end_at']==start['expected_end_at'] and plan['workload_duration_seconds']==7200)
check('exact open tracking Issue preflight',data['issue-preflight.json']['status']=='ready' and data['issue-preflight.json']['issue_number']==1025 and data['issue-preflight.json']['request_id']==REQUEST)
unit=data['attempt-1/pre-clock-generation-1/unit.json']
check('real empty-Job input accepted',unit['valid'] is True and unit['sha256']=='57c3e320a832417c5e803553961f261771af23813115c207218162f214488524' and unit['field_sha256']['Job']==sha(b'') and unit['properties']['Job']==0 and unit['properties']['ActiveState']=='active' and unit['properties']['SubState']=='running' and not unit['invalid_fields'] and not unit['missing_fields'])
def fingerprint(public):return 'SHA256:'+base64.b64encode(hashlib.sha256(base64.b64decode(public.split()[1])).digest()).decode().rstrip('=')
ident=data['encrypted-deployment-identity.json']; access=data['encrypted-access.json']; local=read(ROOT/'local-diagnostic-identity.json')
for n,x in [('encrypted-deployment-identity.json',ident),('encrypted-access.json',access)]:
 check('sealed identity public fields '+n, x['request_id']==REQUEST and x['lease_id']==LEASE and x['source_sha']==SRC and x['algorithm']=='x25519-xsalsa20-poly1305-sealed-box' and len(base64.b64decode(x['ciphertext_base64'],validate=True))>48)
check('diagnostic recipient matches generated-once public identity',access['recipient_fingerprint']==fingerprint(local['public_key']))
check('deployment envelope exact Lease/digest/expiry/public-key',ident['plan_digest']==r['plan_digest'] and ident['expires_at']==r['expires_at'] and ident['deployment_fingerprint']==fingerprint(ident['deployment_public_key']) and ident['deployment_public_key'] in plan['bootstrap_access']['authorized_keys'])
check('diagnostic public key admitted to bootstrap', ' '.join(local['public_key'].split()[:2]) in plan['bootstrap_access']['authorized_keys'])
check('access envelope deployment-plan binding',access['deployment_plan_digest']==dp['plan_digest'])
archive_rows=read(LIVE/'deployment/verified/archive-verification.json')['members']; byname={x['name']:x for x in archive_rows}
check('encrypted envelopes exact deployment→handoff bytes',all(sha(payload[n])==byname[n]['sha256'] for n in ['encrypted-deployment-identity.json','encrypted-access.json']))
save('encrypted-public-identity-binding.json',{'envelopes':[{'name':n,'bytes':len(payload[n]),'sha256':sha(payload[n]),'public_identity':{k:v for k,v in data[n].items() if k not in ['ciphertext_base64','deployment_public_key']}} for n in ['encrypted-deployment-identity.json','encrypted-access.json']],'public_identity_bound':True,'ciphertext_decrypted':False,'private_keys_read':False,'wrapping_secret_match_independently_proved':False})
save('archive-verification.json',{'artifact_id':meta['id'],'complete_actual_size':len(raw),'actual_zip_sha256':sha(raw),'api_digest_match':True,'zip_crc_verified':True,'members':records,'selected_public_payloads':len(selected),'encrypted_payloads_copied':False})
save('identity-verification.json',{'verified_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source_sha':SRC,'request_id':REQUEST,'checks_passed':checks,'started_at':start['started_at'],'expected_end_at':start['expected_end_at'],'deployment_observed_at':ready['observed_at'],'budget':budget,'quoted_reserve_micros':74477600,'prior_task_reserve_micros':223432800,'reserve_is_not_bill':True,'old_task_reserve_is_not_this_request_prior':True,'run_start_contract':'Typed post-sync/post-first-grant receipt from byte-bound source; does not contain independent live counters. Exact source coordinator waits all3 traffic-ready and successful global grant before Begin writes receipt. Frozen bundle YAML config is10000 online/2000 SEND per second/zero completed sync coverage.','two_hour_result':'not_observed','formal_result':'not_observed','current_provider_inventory_independently_queried':False,'encrypted_secrets_decrypted':False})
print(json.dumps({'checks_passed':len(checks),'started_at':start['started_at'],'expected_end_at':start['expected_end_at'],'selected_public_payloads':len(selected),'two_hour_result':'not_observed'}))
