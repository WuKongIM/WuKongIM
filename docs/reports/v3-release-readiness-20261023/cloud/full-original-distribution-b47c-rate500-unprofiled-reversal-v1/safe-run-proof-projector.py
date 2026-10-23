"""Offline fixed-path safe projection; never invokes HTTP, DB, Go or trace decoding."""
from pathlib import Path
import datetime,hashlib,json,os,re,subprocess
R=Path(__file__).resolve().parent
B=R.parent
child_env={'PATH':'/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin','LANG':'C','LC_ALL':'C','TZ':'UTC'}
H=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
def write(name,value):
 path=R/name
 raw=(json.dumps(value,indent=2)+'\n').encode()
 if path.exists():
  assert path.read_bytes()==raw,'existing_safe_projection_mismatch'
  return
 fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
 with os.fdopen(fd,'wb') as stream:stream.write(raw)
def js(name,cap=2<<20):
 path=R/name;assert path.is_file() and not path.is_symlink() and path.stat().st_size<=cap
 return json.loads(path.read_bytes())
def unsigned(value):
 assert type(value) is int and 0<=value<1<<64
 return value
def walk(value):
 yield value
 if isinstance(value,dict):
  for child in value.values():yield from walk(child)
 elif isinstance(value,list):
  for child in value:yield from walk(child)
# Only fixed coordinator JSON records are read. Arbitrary private text is not copied.
log=R/'logs/coordinator.log';log_before=(H(log),log.stat().st_mtime_ns);text=log.read_text();assert len(text)<=1<<20
objects=[];position=0;decoder=json.JSONDecoder()
while True:
 position=text.find('{',position)
 if position<0:break
 try:value,end=decoder.raw_decode(text[position:]);objects.append(value);position+=end
 except ValueError:position+=1
start=js('report/run-start.json');start_time=datetime.datetime.fromisoformat(start['started_at']);cuts=[]
msgkeys=['sent','send_attempts','first_attempts','first_attempt_failures','send_acknowledged','send_rejected','received','receive_acknowledged','receive_ack_failures','retry_attempts','terminal','losses','duplicates','corruptions','sequence_regressions']
for value in objects:
 if not isinstance(value,dict) or not isinstance(value.get('messages'),dict):continue
 assert value['cut'] in ['periodic','terminal']
 messages={key:unsigned(value['messages'][key]) for key in msgkeys}
 at=datetime.datetime.fromisoformat(value['at'].replace('Z','+00:00'));elapsed=(at-start_time).total_seconds();assert 0<=elapsed<=600
 cuts.append({'at':value['at'],'cut':value['cut'],'elapsed_since_firstgrant_seconds':elapsed,'messages':messages,'offered_first_attempts_per_elapsed_second':messages['first_attempts']/elapsed,'rate_semantics':'snapshot aggregate divided by elapsed firstgrant clock; terminal may include drain and is not active SEND QPS'})
assert len(cuts)<=128 and cuts and cuts[-1]['cut']=='terminal'
write('safe-worker-cuts.json',{'schema':'localb47c-fixed-coordinator-cuts/v1','cuts':cuts})
kinds={'configuration','context_canceled','context_deadline','transport','auth','http_status','response_limit','body_read','parse','mixed','unknown'}
parse={'line_limit','series_limit','invalid_value','incomplete_metadata_results','invalid_sample','invalid_labels','invalid_metadata_labels','invalid_metadata_slot','invalid_metadata_counter','invalid_metadata_result','duplicate_metadata_slot_result','aggregate_overflow','unsupported_family','invalid_queue_labels','invalid_queue_family','duplicate_or_excess_queue_series','unpaired_queue_series','invalid_queue_utilization','invalid_sample_name','label_cardinality_limit','unknown'}
final=js('report/final.json');assert final['final'] is True and final['verdict']['outcome']=='product_failure' and final['verdict']['cause']=='message_loss' and final['verdict']['terminal'] is True
assert unsigned(final['verdict']['cleanup_error_count'])==0
for value in objects:assert not (isinstance(value,dict) and value.get('stage')=='meta_checkpoint' and value.get('reason')=='metrics_fetch'),'unexpected_finalization_failure'
verdict={key:final['verdict'][key] for key in ['outcome','cause','terminal','cleanup_error_count']}
final_messages={key:unsigned(final['messages'][key]) for key in msgkeys};generated={key:unsigned(final['generated'][key]) for key in ['primary','person','group','canary','payload_bytes']}
window={key:final['window'][key] for key in ['start','end','elapsed']};assert type(window['elapsed']) is int and 0<window['elapsed']<=600000000000
write('safe-final-report.json',{'schema':'localb47c-original-final-report-projection/v1','raw_final_present':True,'raw_final_sha256':H(R/'report/final.json'),'verdict':verdict,'messages':final_messages,'generated':generated,'window':window,'finalization_failure_record_present':False,'no_metrics_mask_inferred':True,'formal_or_Linux_acceptance':False})
e=js('report/worker-evidence.json');workers=[]
for worker in e['workers']:
 wid=unsigned(worker['worker_id']);assert wid<3;classes=[]
 for item in worker['evidence']['classes']:
  assert unsigned(item['class'])==3;rows=[]
  for sample in (item.get('first') or [])+(item.get('last') or []):
   assert len(rows)<64 and isinstance(sample['fingerprint'],list) and len(sample['fingerprint'])==16 and all(type(v) is int and 0<=v<=255 for v in sample['fingerprint']);rows.append({'stage':unsigned(sample['stage']),'code':unsigned(sample['code']),'sample_index':str(unsigned(sample['sample_index'])),'fingerprint':bytes(sample['fingerprint']).hex(),'value':unsigned(sample['value'])})
  classes.append({'class':item['class'],'count':unsigned(item['count']),'samples':rows})
 workers.append({'worker_id':wid,'generation':unsigned(worker['generation']),'snapshot_sequence':unsigned(worker['snapshot_sequence']),'classes':classes})
write('safe-worker-evidence.json',{'schema':'localb47c-closed-worker-evidence/v1','workers':workers,'no_identity_reconstruction_or_DB_query':True})
expected_names=sorted(['launcher','watchdog','coordinator','process-metrics-collector','host-metrics-load']+[f'{kind}-{node}' for kind in ['service','worker','host-metrics'] for node in [1,2,3]])
pid_names=sorted(path.stem for path in (R/'pids').glob('*.pid'));assert pid_names==expected_names
assert not any((R/name).exists() for name in ['profile-driver-status.json','profile-driver.claim','profile-phase','traces-three-node','profiles-three-node','evidence/profile-stop','evidence/profile-driver-exit-status'])
write('safe-control-status.json',{'schema':'localb47c-unprofiled-control-status/v1','expected_recorded_process_names':pid_names,'profile_capture_invocations':0,'profile_driver_PID_or_status_created':False,'extra_API_probes':False,'no_B16_width_change':True,'basis':'sealed source diff and static contract;14 recorded PID names and no capture state paths; final ownedPGID zero separately proved'})
resources=[]
for line in (R/'host-process-samples.jsonl').read_text().splitlines():
 item=json.loads(line);assert isinstance(item['other_wukongim_processes'],list) and not item['other_wukongim_processes'];resources.append(item)
assert resources
write('safe-host-resources.json',{'schema':'localb47c-retained-resource-aggregates/v1','sample_count':len(resources),'first_sample_at':resources[0]['at_utc'],'last_sample_at':resources[-1]['at_utc'],'maximum_owned_RSS_sum_bytes':max(sum(row['rss_bytes'] for row in item['owned']) for item in resources),'maximum_owned_process_CPU_percent_sum':max(sum(row['cpu_percent'] for row in item['owned']) for item in resources),'maximum_all_process_CPU_percent_sum':max(item['aggregate_process_cpu_percent'] for item in resources),'other_WK_seen':False,'actual_physical_headroom':None,'limitation':'retained ps percent fields may reflect process lifetime averages; sums and RSS do not establish instantaneous CPU headroom or unique physical memory'})
approval=json.loads((B/'full-original-distribution-b47c-rate500-unprofiled-reversal-root-approval-v1/decision.json').read_text());pins=approval['static_pins'];actual={name:H(R/name) for name in pins};assert actual==pins
write('safe-input-immutability.json',{'schema':'localb47c-runtime-input-immutability/v1','approval_manifest_sha256':H(B/'full-original-distribution-b47c-rate500-unprofiled-reversal-root-approval-v1/SHA256SUMS'),'static_input_sha256':actual,'all_equal_approved_pins':True,'proposal_manifest_sha256':H(B/'full-original-distribution-b47c-rate500-unprofiled-reversal-proposal-v1/PUBLIC_SHA256SUMS'),'source_revision':approval['source'],'source_tree':approval['tree']})
cleanup=js('cleanup-receipt.json');assert cleanup['complete'] and cleanup['owned_pgid']==7232 and cleanup['remaining_processes']==[] and cleanup['remaining_listeners']==[] and cleanup['tested_ports']==list(range(25000,25064));write('safe-cleanup-receipt.json',cleanup)
processes=[]
for line in subprocess.check_output(['ps','-axo','pid=,pgid=,comm='],text=True,timeout=5,env=child_env).splitlines():
 fields=line.split(None,2)
 if len(fields)==3 and (int(fields[1])==7232 or Path(fields[2]).name in ['wukongim','wkcli']):processes.append({'pid':int(fields[0]),'pgid':int(fields[1]),'process':Path(fields[2]).name})
listeners=subprocess.run(['lsof','-nP','-iTCP:25000-25063','-sTCP:LISTEN'],capture_output=True,text=True,timeout=5,env=child_env);assert listeners.returncode in [0,1] and not listeners.stdout and not processes
write('safe-independent-zero.json',{'schema':'localb47c-postcleanup-independent-zero/v1','at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'owned_pgid':7232,'owned_group_and_other_WK':processes,'all64_selected_LISTEN_ports_zero':True,'method':'ps closed process names and lsof LISTEN; no service/HTTP probe'})
metrics={}
for label in ['first-measured','terminal']:
 items=js(f'evidence/metrics-{label}-receipt.json');assert len(items)==3
 for item in items:assert set(item)<=set(['node','complete','bytes','sha256','error_class']) and item['node'] in [1,2,3] and type(item['complete']) is bool
 metrics[label]=items
write('safe-metrics-receipts.json',{'schema':'localb47c-existing-metrics-receipts/v1','receipts':metrics,'no_new_metrics_requests':True})
files=['logs/coordinator.log','logs/service-1.log','logs/service-2.log','logs/service-3.log','report/worker-evidence.json','report/diagnostic-status.json','report/run-start.json','diagnostic-result.json','report/final.json','supervisor-events.json','host-process-samples.jsonl','evidence/metrics-first-measured-node1.prom','evidence/metrics-first-measured-node2.prom','evidence/metrics-first-measured-node3.prom','evidence/metrics-terminal-node1.prom','evidence/metrics-terminal-node2.prom','evidence/metrics-terminal-node3.prom']
artifacts=[]
for name in files:
 path=R/name;stat=path.stat();artifacts.append({'private_path':str(path),'bytes':stat.st_size,'mtime_ns':stat.st_mtime_ns,'sha256':H(path)})
assert (H(log),log.stat().st_mtime_ns)==log_before
write('safe-private-artifact-inventory.json',{'schema':'localb47c-fixed-private-input-inventory/v1','artifacts':artifacts,'raw_inputs_not_public':True,'coordinator_log_unchanged_through_projection':True})
result=js('diagnostic-result.json');firstloss=next(row for row in cuts if row['messages']['losses']);assert result['launcher_exit_status']==7 and result['final_report_present'] is True and result['original_verdict']['outcome']=='product_failure' and result['original_verdict']['cause']=='message_loss'
write('safe-summary.json',{'schema':'localb47c-unprofiled-diagnostic-summary/v1','source_revision':approval['source'],'source_tree':approval['tree'],'supervisor_started_at_utc':result['started_at_utc'],'first_grant_at':start['started_at'],'terminal_cut_at':cuts[-1]['at'],'launcher_exit_status':7,'final_report_present':True,'original_final_verdict':verdict,'finalization_failure_record_present':False,'first_cut':cuts[0],'first_loss_cut':firstloss,'terminal_cut':cuts[-1],'generated':generated,'final_window':window,'profile_captures':0,'cleanup_complete':True,'runtime_seconds':cleanup['elapsed_seconds'],'config_sha256':actual['original-distribution-local.yaml'],'configured_SEND_per_second':500,'target_measured_seconds':120,'limits_unchanged':True,'no_rerun':True,'no_DB_or_new_service_or_HTTP_after_stop':True,'topology_clarification':'256 HashSlots and12 physical Slot Raft identities, unchanged source/config; prior proposal logical-slot phrasing corrected by root approval','limitations':['native shared Macarm64 diagnostic, not Linux/formal acceptance','first failure stopped original120second target early','unprofiled run still proves message_loss under this local workload, but not the exact cloud root cause','coherent metrics collector now permits this proper product_failure final report; no finalization failure or parser mask is invented','no exact identity reconstruction or DB read was performed']})
print(json.dumps({'safe_payloads_written':11,'input_immutable':True,'process_and_ports_zero':True,'actual_final_verdict':verdict,'actual_loss_count':final_messages['losses'],'profile_captures':0}))
