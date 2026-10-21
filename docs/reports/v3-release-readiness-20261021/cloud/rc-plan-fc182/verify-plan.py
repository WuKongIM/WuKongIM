#!/usr/bin/env python3
"""Offline contract check; deliberately cannot authorize or perform dispatch."""
import argparse,hashlib,json,re,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--repo',required=True,type=Path);p.add_argument('--plan',required=True,type=Path);a=p.parse_args()
v=json.loads(a.plan.read_bytes());sha=v['identity']['reviewed_head_sha']
assert re.fullmatch('[0-9a-f]{40}',sha)
def raw(name):return subprocess.check_output(['git','-C',str(a.repo),'show',sha+':'+name])
for name,fact in v['source_files'].items():
 b=raw(name);assert {'sha256':hashlib.sha256(b).hexdigest(),'size':len(b)}==fact,name
assert v['status']=='draft_not_dispatchable' and not v['authorizations']['paid_authorization_received']
assert v['identity']['bundle_digest'] is None
merged=v['identity']['merged_source_sha']
if merged:
 assert subprocess.check_output(['git','-C',str(a.repo),'rev-parse',merged+'^{tree}']).decode().strip()==v['identity']['reviewed_head_tree']
for stage,seconds,hours in [('rehearsal',7200,12),('formal',259200,96)]:
 t=json.loads(raw(f'configs/cloud/chat-lifecycle/{stage}-v1.json'))
 assert t==v['run_plan_templates'][stage]
 assert t['budget']=={k:v['budget'][k] for k in ['currency','hard_limit_micros','operational_stop_micros']}
 assert t['workload_duration_seconds']==seconds and t['lease_duration_seconds']==hours*3600
 assert t['compute']=={'vcpus':4,'memory_bytes':8<<30,'architecture':'x86_64','billing_model':'postpaid','allow_burstable':False}
 assert t['host_groups']==[{'role':'service','count':3,'system_disk_bytes':40<<30,'data_disk_bytes':500<<30,'public_ipv4':False},{'role':'load','count':1,'system_disk_bytes':40<<30,'data_disk_bytes':200<<30,'public_ipv4':True}]
 assert t['retry']['deployment_retries']==0
workflow=raw('.github/workflows/chat-lifecycle-rehearsal.yml').decode()
inputs=re.findall(r'^      ([a-z_]+):$',workflow.split('\npermissions:',1)[0],re.M)
assert set(inputs)==set(v['top_level_inputs'])=={'source_sha','operator','codex_diagnostic_pubkey','request_id'}
assert v['top_level_inputs']=={'source_sha':merged,'operator':'tangtaoit','codex_diagnostic_pubkey':None,'request_id':None}
formal=raw('configs/wkbench/chat-lifecycle/formal.yaml').decode()
rehearsal=raw('configs/wkbench/chat-lifecycle/rehearsal.yaml').decode()
def normalize(s):return '\n'.join(l for l in s.splitlines() if not l.startswith(('run_id:','stage:')))
assert normalize(formal)==normalize(rehearsal)
for token in ['online_users: 10000','bootstrap_logins_per_second: 100','send_rate_per_second: 2000','hot_sendack: {p99: 400ms, p999: 1s}','maximum_duration: 8h','recovery_duration: 30m']:
 assert token in formal,token
assert v['stages'][1]['admission_minimum_remaining_seconds']==(72+8+.5+1)*3600
assert v['local_shakeout_reference']['runtime_paths_diff_to_ab75']==[]
assert not v['prior_bundle']['valid_for_new_candidate']
print(json.dumps({'schema':'wukongim.rc_cloud_draft_check/v1','passed':True,'source_sha':sha,'source_files_verified':len(v['source_files']),'provider_called':False,'network_called':False,'dispatch_ready':False,'merged_source_sha':merged,'reason':'explicit paid authorization, live prerequisites and new exact bundle remain pending'},ensure_ascii=False))
