#!/usr/bin/env python3
"""Summarize existing GET-only receipts. No network, provider or repository writes."""
import argparse, hashlib, json
from pathlib import Path
p=argparse.ArgumentParser(); p.add_argument('--snapshot',type=Path,required=True); p.add_argument('--previous',type=Path,required=True); p.add_argument('--plan',type=Path,required=True); p.add_argument('--output',type=Path,required=True); a=p.parse_args()
def read(n,root=None): return json.loads(((root or a.snapshot)/n).read_bytes())
def data(n,root=None):
 r=read(n,root); assert r['exit_code']==0,n; return r['data']
src=data('source-commit.json'); before=data('main-before.json')['object']['sha']; after=data('main-after.json')['object']['sha']; assert before==after==src['sha']=='fc18221282f289088ec9b4282fda8e0bfc67a3b8'
plan=json.loads(a.plan.read_bytes()); assert plan['identity']['merged_source_sha']==src['sha']; assert plan['identity']['merged_source_tree']==src['tree']['sha']
vs=[data(f.name) for f in sorted(a.snapshot.glob('variable-*.json'))]; assert len(vs)==8 and all(v['present'] and v['equals_0456_snapshot'] for v in vs)
ws=[data(f.name) for f in sorted(a.snapshot.glob('workflow-*.json'))]; wfchanges=[w['path'] for w in ws if w['state']!=data('workflow-'+w['path'].split('/')[-1]+'.json',a.previous)['state']]
envs=[data(f.name) for f in sorted(a.snapshot.glob('environment-*.json'))]
metadata={f.name:read(f.name) for f in sorted(a.snapshot.glob('*-metadata.json')) if f.name!='retained-artifact-metadata.json'}
assert all(x['complete_paginated_snapshot'] and x['total_count_stable'] for x in metadata.values())
active=read('nonterminal-runs.json'); assert all(x['complete_paginated_snapshot'] and x['total_count_stable'] for x in active['filters'])
ar=read('retained-artifact-metadata.json'); assert ar['complete_paginated_snapshot'] and ar['total_count_stable'] and ar['inventory_count_matches_advertised'] and ar['unique_ids']==ar['total_rows_observed']
old=read('artifact-inventory.json',a.previous); oldrows=[r for r in old['artifacts'] if r['name'].startswith(tuple(ar['selection_prefixes']))]
oldids={r['id'] for r in oldrows}; ids={r['id'] for r in ar['rows']}
signals={k:[r for r in ar['rows'] if k in r['name']] for k in ['handoff','transition','cleanup-pending','operator-stop']}
result={
 'schema':'wukongim.rc_github_readiness_snapshot/v1','status':'read_only_snapshot_ready_for_paid_scope_review_not_execution',
 'observation_utc':{'started_at':min(read(f.name)['started_at'] for f in a.snapshot.glob('*.json') if 'started_at' in read(f.name)),'finished_at':read('main-after.json')['observed_at']},
 'scope':{'github_get_only':True,'secret_values_requested':False,'nonallowlisted_variable_values_discarded':True,'account_arn_ids_redacted':True,'artifact_payload_downloaded':False,'provider_called':False,'quote_called':False,'dispatch_or_enable':False,'permissions_modified':False,'keys_created_or_read':False},
 'source':{'sha':src['sha'],'tree':src['tree']['sha'],'parents':[r['sha'] for r in src['parents']],'main_before':before,'main_after':after,'previous_main':data('main-ref.json',a.previous)['object']['sha'],'unrelated_local_main_not_used':True},
 'identity_variables':[{'name':r['name'],'present':r['present'],'value_sha256':r['value_sha256'],'equals_0456_snapshot':r['equals_0456_snapshot']} for r in vs],
 'oidc_subject':data('oidc-subject.json'),'oidc_subject_unchanged':data('oidc-subject.json')==data('oidc-subject.json',a.previous),
 'environments':envs,'environment_protection_unchanged':all(all(e[k]==data('environment-'+e['name']+'.json',a.previous)[k] for k in ['id','protection_rules','deployment_branch_policy']) for e in envs),
 'main_protection_unchanged':data('main-protection.json')==data('main-protection.json',a.previous),'main_protection':data('main-protection.json'),'rulesets':data('rulesets.json'),
 'metadata':{n:{'total_count':v['pages'][-1]['total_count'],'allowlisted_names':[x['name'] for x in v['rows']] if '-secrets-' in n else None,'non_allowlisted_secret_metadata_count':v.get('non_allowlisted_metadata_count'),'complete':v['complete_paginated_snapshot']} for n,v in metadata.items()},
 'workflow_states':{w['path'].split('/')[-1]:w['state'] for w in ws},'workflow_state_changes_since_0456':wfchanges,
 'nonterminal_runs':{x['status_filter']:{'count':len(x['rows']),'pages':len(x['pages']),'complete':x['complete_paginated_snapshot']} for x in active['filters']},
 'artifact_inventory':{'total_observed':ar['total_rows_observed'],'unique_ids':ar['unique_ids'],'pages':len(ar['pages']),'per_page':ar['per_page'],'page_limit':ar['page_limit'],'complete':ar['complete_paginated_snapshot'],'selected_count':len(ar['rows']),'previous_total':old['pages'][-1]['total_count'],'previous_selected_count':len(oldrows),'selected_ids_added':sorted(ids-oldids),'selected_ids_removed':sorted(oldids-ids),'signals':signals,'cleanup_named_count':sum('cleanup' in r['name'] for r in ar['rows']),'payloads_downloaded':False},
 'prior_offline_plan':{'path':str(a.plan),'sha256':hashlib.sha256(a.plan.read_bytes()).hexdigest(),'source_matches':True,'budget':plan['budget'],'stages':plan['stages']},
 'authorization':plan['authorizations'],
 'runtime_checks_deferred_to_authorized_fail_closed_start':['Request uniqueness and fresh control/source/artifact binding','Live OIDC three-role exchange, exact trust/policy/session checks and wrapping key match','Backstop arming by existing owner before Quote/Acquire','Current provider exact-selector inventory/zero proofs, admitted Quote budget/capacity/quota','One top-level-owned bundle for final source; no duplicate standalone CI build'],
 'limitations':['Sequential non-atomic GitHub snapshot, not a promise of later state','Metadata names and role variables do not prove live IAM or secret/key usability','No retained handoff/pending container does not prove provider zero; deleted/expired historical evidence is outside visible inventory','Old stop markers remain historical and must not be reused as a new request','No payload downloaded or old zero proofs reauthenticated here; no new readiness blocker inferred from unperformed live checks']}
with a.output.open('x') as f:json.dump(result,f,ensure_ascii=False,indent=2);f.write('\n')
print(json.dumps({'output':str(a.output),'source':src['sha'],'vars_present_unchanged':len(vs),'active':sum(len(x['rows']) for x in active['filters']),'selected_artifact_ids_unchanged':ids==oldids,'stop_markers':len(signals['operator-stop']),'workflow_changes':wfchanges}))
