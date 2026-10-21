#!/usr/bin/env python3
"""Offline synthesis; existing evidence only, never calls cloud or GitHub."""
import base64,collections,hashlib,json,pathlib,re
P=pathlib.Path(__file__).resolve().parent
load=lambda n:json.loads((P/n).read_text())
variables={p.stem.removeprefix('variable-'):json.loads(p.read_text())['data']['value'] for p in P.glob('variable-*.json')}
account='50175678';checks=[]
def check(name,value):checks.append({'name':name,'pass':bool(value)})
check('seven identity variables and wrapping public key',len(variables)==8)
check('region cn-hangzhou',variables['ALIBABA_CLOUD_LEASE_REGION']=='cn-hangzhou')
check('provider exact repository-owned name',variables['ALIBABA_CLOUD_LEASE_OIDC_PROVIDER_ARN']==f'acs:ram::{account}:oidc-provider/wukongim-cloud-lease-github')
check('audience',variables['ALIBABA_CLOUD_LEASE_OIDC_AUDIENCE']=='wukongim-cloud-lease')
check('account hash matches public ARN account',variables['ALIBABA_CLOUD_LEASE_ACCOUNT_ID_HASH']=='sha256:'+hashlib.sha256(account.encode()).hexdigest())
subjects={}
for label,role,env,workflow in [('PROVISIONER','CloudLeaseProvisioner','cloud-lease-provision','cloud-lease-provision.yml'),('OBSERVER','CloudLeaseObserver','cloud-lease-observe','cloud-lease-observe.yml'),('RELEASER','CloudLeaseReleaser','cloud-lease-release','cloud-lease-release.yml')]:
 check('role ARN '+label,variables['ALIBABA_CLOUD_LEASE_'+label+'_ROLE_ARN']==f'acs:ram::{account}:role/{role.lower()}')
 subjects[role]=[f'repo:WuKongIM/WuKongIM:environment:{env}:job_workflow_ref:WuKongIM/WuKongIM/.github/workflows/{w}@refs/heads/main' for w in ['cloud-lease-oidc-setup.yml',workflow,'cloud-lease-analyze.yml']]
oidc=load('oidc-subject.json')['data'];check('GitHub subject customization exact',oidc['use_default'] is False and oidc.get('use_immutable_subject',False) is False and oidc['include_claim_keys']==['repo','context','job_workflow_ref'])
key=variables['WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY'].split();check('public key type',key[0]=='ssh-ed25519');fingerprint='SHA256:'+base64.b64encode(hashlib.sha256(base64.b64decode(key[1])).digest()).decode().rstrip('=')
scopes={'disk_attachments','disks','eip_associations','eips','enis','instances','nat_gateways','route_entries','security_group_rules','security_groups','vpcs','vswitches'}
rows=[]
for f in sorted((P/'cleanup-selected-json').glob('*.json')):
 d=json.loads(f.read_text());j=d['allowlisted_json'];c=j['cleanup.json'];z=j['zero-inventory.json'];zero=z['result']['zero_inventory'];s=zero['selector']
 valid=(c['schema']=='wukongim.chat_lifecycle.rehearsal_cleanup/v1' and c['zero_inventory'] is True and isinstance(c['release_run_id'],int) and c['release_run_id']>0 and z['schema']=='wukongim.cloud_lease.release/v1' and c['request_id']==s['request_id'] and s['provider']=='alibaba' and s['region']=='cn-hangzhou' and s['repository']=='WuKongIM/WuKongIM' and re.fullmatch('[0-9a-f]{64}',s['plan_digest']) is not None and zero['account_id_hash']==variables['ALIBABA_CLOUD_LEASE_ACCOUNT_ID_HASH'] and set(zero['scopes'])==scopes and len(zero['scopes'])==12 and isinstance(zero['observed_at'],str))
 independent=j.get('release-selector.json');bind=(independent.get('selector')==s) if independent else None
 rows.append({'artifact_id':d['artifact_id'],'request_id':c['request_id'],'structural_check_pass':bool(valid),'selector':s,'observed_at':zero['observed_at'],'same_artifact_selector_matches':bind,'full_producer_and_complete_handoff_authentication_performed':False})
check('27 retained cleanup JSON shapes/selectors/account/scopes',len(rows)==27 and all(r['structural_check_pass'] for r in rows))
arts=load('artifact-inventory.json')['artifacts'];handoffs=[a for a in arts if not a['expired'] and (a['name'].startswith('chat-lifecycle-rehearsal-handoff-') or a['name'].startswith('chat-lifecycle-formal-handoff-'))]
rr=load('rehearsal-request-runs.json');clean={r['request_id'] for r in rows};unmatched=[r for r in rr['workflow_runs'] if r['display_title'].removeprefix('Chat Lifecycle Rehearsal ') not in clean]
summary={'scope':'GitHub read-only configuration and retained historical evidence; no dispatch/provider/quote/secret values','github_main_snapshot':load('main-ref.json'),'configuration_checks':checks,'all_configuration_and_payload_shape_checks_pass':all(c['pass'] for c in checks),'live_cloud_verified':False,'current_provider_zero_inventory_proved':False,'expected_trust_subjects_only_not_observed_RAM_trust':subjects,'wrapping_public_key_fingerprint':fingerprint,'wrapping_pair_live_verified_today':False,'workflows':[{k:json.loads(f.read_text())['data'][k] for k in ['name','path','state']} for f in sorted(P.glob('workflow-*.json'))],'artifact_inventory_count':len(arts),'retained_formal_or_rehearsal_handoffs':handoffs,'historical_rehearsal_cleanup_shape_checks':rows,'rehearsal_runs_without_retained_matching_cleanup':unmatched,'issues_found':len(load('chat-lifecycle-issue-metadata.json')),'open_issues_found':[i for i in load('chat-lifecycle-issue-metadata.json') if i['state']=='OPEN'],'nonterminal_runs':load('nonterminal-runs.json'),'limitations':['GitHub and historical proofs cannot establish current Alibaba inventory or permissions.','No cloud-side role trust/policy/session call was made.','27 selected cleanup payloads were structure-checked; complete handoff/producer authentication was not repeated.','Artifacts may have been deleted or expired; absent handoff is not provider-zero proof.','Public variables and secret names do not prove matching key pair, successful role exchange, funds, quota, placement or cleanup operation.','Candidate source/control/bundle and applicable instruction digests must freeze after merge.']}
summary['early_unmatched_request_followup']=[{'rehearsal_run_id':parent,'provision_run_id':runid,'quote_step_conclusion':'failure','acquire_step_conclusion':'skipped','evidence_jobs_file':'jobs-'+runid+'.json','evidence_error_file':'early-provision-'+runid+'-error-excerpt.json'} for parent,runid in [('31261098792','31261198276'),('31260364699','31260469032'),('31259617695','31259766615')]]
for followup in summary['early_unmatched_request_followup']:
    job=load(followup['evidence_jobs_file'])['jobs'][0]
    assert any(s['name']=='Quote exact capacity and budget' and s['conclusion']=='failure' for s in job['steps'])
    assert any(s['name']=='Acquire explicitly authorized paid Lease' and s['conclusion']=='skipped' for s in job['steps'])
(P/'readiness-audit-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps({'checks':len(checks),'all_pass':summary['all_configuration_and_payload_shape_checks_pass'],'retained_cleanup':len(rows),'independent_selector_members':sum(r['same_artifact_selector_matches'] is True for r in rows),'unmatched_runs':len(unmatched),'wrapping_fingerprint':fingerprint},indent=2))
