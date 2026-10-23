"""Offline fixed-path safe projection; never invokes HTTP, DB, Go or trace decoding."""
from pathlib import Path
import datetime,hashlib,json,os,re,subprocess
R=Path(__file__).resolve().parent
B=R.parent
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
write('safe-worker-cuts.json',{'schema':'local2662-fixed-coordinator-cuts/v1','cuts':cuts})
kinds={'configuration','context_canceled','context_deadline','transport','auth','http_status','response_limit','body_read','parse','mixed','unknown'}
parse={'line_limit','series_limit','invalid_value','incomplete_metadata_results','invalid_sample','invalid_labels','invalid_metadata_labels','invalid_metadata_slot','invalid_metadata_counter','invalid_metadata_result','duplicate_metadata_slot_result','aggregate_overflow','unsupported_family','invalid_queue_labels','invalid_queue_family','duplicate_or_excess_queue_series','unpaired_queue_series','invalid_queue_utilization','invalid_sample_name','label_cardinality_limit','unknown'}
finals=[]
for value in objects:
 if not isinstance(value,dict) or value.get('stage')!='meta_checkpoint' or value.get('reason')!='metrics_fetch':continue
 m=value['metrics_fetch'];mask=m['source_mask'];ks=m['kinds'];assert type(mask) is int and 1<=mask<=7 and isinstance(ks,list) and len(ks)==3 and all(isinstance(k,str) and ((k in kinds) if mask&(1<<i) else k=='') for i,k in enumerate(ks))
 row={k:value[k] for k in ['stage','reason','observed_outcome','observed_cause','observed_terminal']};assert row['observed_outcome'] in ['product_failure','infrastructure_failure','harness_invalid','operator_stopped','pass'] and row['observed_cause'] in ['message_loss','worker_product','none','unknown'] and type(row['observed_terminal']) is bool
 row['metrics_fetch']={'source_mask':mask,'kinds':ks};prs=m.get('parse_reasons')
 if prs is not None:
  assert isinstance(prs,list) and len(prs)==3 and all(isinstance(v,str) and len(v)<=64 and ((v in parse) if ks[i]=='parse' else v=='') for i,v in enumerate(prs));row['metrics_fetch']['parse_reasons']=prs
 finals.append(row)
assert len(finals)==1
write('safe-finalization.json',{'schema':'local2662-closed-finalization/v1','finalization':finals[0],'final_report_present':(R/'report/final.json').exists(),'limitation':'prior observed product failure remains observation; exit8 and absent final report are not a complete final verdict'})
e=js('report/worker-evidence.json');workers=[]
for worker in e['workers']:
 wid=unsigned(worker['worker_id']);assert wid<3;classes=[]
 for item in worker['evidence']['classes']:
  assert unsigned(item['class'])==3;rows=[]
  for sample in (item.get('first') or [])+(item.get('last') or []):
   assert len(rows)<64 and isinstance(sample['fingerprint'],list) and len(sample['fingerprint'])==16 and all(type(v) is int and 0<=v<=255 for v in sample['fingerprint']);rows.append({'stage':unsigned(sample['stage']),'code':unsigned(sample['code']),'sample_index':str(unsigned(sample['sample_index'])),'fingerprint':bytes(sample['fingerprint']).hex(),'value':unsigned(sample['value'])})
  classes.append({'class':item['class'],'count':unsigned(item['count']),'samples':rows})
 workers.append({'worker_id':wid,'generation':unsigned(worker['generation']),'snapshot_sequence':unsigned(worker['snapshot_sequence']),'classes':classes})
write('safe-worker-evidence.json',{'schema':'local2662-closed-worker-evidence/v1','workers':workers,'no_identity_reconstruction_or_DB_query':True})
traces=[]
for node in [1,2,3]:
 directory=R/f'traces-three-node/node-{node}';path=directory/f'traces/node-{node}-execution.trace';metadata=directory/f'requests/node-{node}-execution-trace.json';item=json.loads(metadata.read_bytes());before=path.stat();sha=hashlib.sha256()
 with path.open('rb') as stream:
  while True:
   chunk=stream.read(65536)
   if not chunk:break
   sha.update(chunk)
 after=path.stat();assert (before.st_size,before.st_mtime_ns,before.st_ino,before.st_dev)==(after.st_size,after.st_mtime_ns,after.st_ino,after.st_dev)
 assert sha.hexdigest()==item['sha256'] and before.st_size==item['retained_body_bytes']<=67108864 and (before.st_mode&0o777)==0o600 and (directory.stat().st_mode&0o777)==0o700
 assert item['status']=='transfer_complete' and item['reason']=='ok' and item['curl_exit_code']==0 and item['http_status']=='200' and item['curl_joined'] is True and item['trace_format_valid'] is None and item['trace_decoded'] is False
 traces.append({'node_id':node,'private_trace_path':str(path),'private_request_metadata_path':str(metadata),'request_metadata_sha256':H(metadata),'sha256':sha.hexdigest(),'bytes':before.st_size,'mtime_ns':before.st_mtime_ns,'dev':before.st_dev,'inode':before.st_ino,'mode_octal':'0600','private_output_dir_mode':'0700',**{k:item[k] for k in ['status','reason','curl_exit_code','http_status','curl_joined','curl_launch_parent_observed_at','completed_at_utc','trace_format_valid','trace_decoded']}})
write('safe-trace-manifest.json',{'schema':'local2662-three-private-traces/v1','traces':traces,'total_bytes':sum(row['bytes'] for row in traces),'trace_format_valid':None,'trace_decoded':False,'operation':'bounded SHA only; no trace format or records decoded','receiver_limit_is_not_server_memory_bound':True})
sampler=js('profile-driver-status.json');assert sampler['status']=='transfer_complete' and sampler['trace_format_valid'] is None and sampler['trace_decoded'] is False
write('safe-sampler-status.json',sampler)
approval=json.loads((B/'full-original-distribution-2662-rate500-three-trace-root-approval-v1/decision.json').read_text());pins=approval['runtime_controls'];actual={name:H(R/name) for name in pins};assert actual==pins
write('safe-input-immutability.json',{'schema':'local2662-runtime-input-immutability/v1','approval_manifest_sha256':H(B/'full-original-distribution-2662-rate500-three-trace-root-approval-v1/SHA256SUMS'),'static_input_sha256':actual,'all_equal_approved_pins':True,'proposal_manifest_sha256':H(B/'full-original-distribution-2662-rate500-three-trace-proposal-v1/PUBLIC_SHA256SUMS'),'sampler_manifest_sha256':H(B/'execution-trace-first-grant-proposal-v1/PUBLIC_SHA256SUMS'),'source_revision':approval['candidate'],'source_tree':approval['tree']})
cleanup=js('cleanup-receipt.json');assert cleanup['complete'] and cleanup['owned_pgid']==78820 and cleanup['remaining_processes']==[] and cleanup['remaining_listeners']==[] and cleanup['tested_ports']==list(range(25000,25064));write('safe-cleanup-receipt.json',cleanup)
processes=[]
for line in subprocess.check_output(['ps','-axo','pid=,pgid=,comm='],text=True,timeout=5).splitlines():
 fields=line.split(None,2)
 if len(fields)==3 and (int(fields[1])==78820 or Path(fields[2]).name in ['wukongim','wkcli']):processes.append({'pid':int(fields[0]),'pgid':int(fields[1]),'process':Path(fields[2]).name})
listeners=subprocess.run(['lsof','-nP','-iTCP:25000-25063','-sTCP:LISTEN'],capture_output=True,text=True,timeout=5);assert listeners.returncode in [0,1] and not listeners.stdout and not processes
write('safe-independent-zero.json',{'schema':'local2662-postcleanup-independent-zero/v1','at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'owned_pgid':78820,'owned_group_and_other_WK':processes,'all64_selected_LISTEN_ports_zero':True,'method':'ps closed process names and lsof LISTEN; no service/HTTP probe'})
metrics={}
for label in ['first-measured','terminal']:
 items=js(f'evidence/metrics-{label}-receipt.json');assert len(items)==3
 for item in items:assert set(item)<=set(['node','complete','bytes','sha256','error_class']) and item['node'] in [1,2,3] and type(item['complete']) is bool
 metrics[label]=items
write('safe-metrics-receipts.json',{'schema':'local2662-existing-metrics-receipts/v1','receipts':metrics,'no_new_metrics_requests':True})
files=['logs/coordinator.log','logs/service-1.log','logs/service-2.log','logs/service-3.log','report/worker-evidence.json','report/diagnostic-status.json','report/run-start.json','diagnostic-result.json','supervisor-events.json','host-process-samples.jsonl','evidence/metrics-first-measured-node1.prom','evidence/metrics-first-measured-node2.prom','evidence/metrics-first-measured-node3.prom','evidence/metrics-terminal-node1.prom','evidence/metrics-terminal-node2.prom','evidence/metrics-terminal-node3.prom']
artifacts=[]
for name in files:
 path=R/name;stat=path.stat();artifacts.append({'private_path':str(path),'bytes':stat.st_size,'mtime_ns':stat.st_mtime_ns,'sha256':H(path)})
assert (H(log),log.stat().st_mtime_ns)==log_before
write('safe-private-artifact-inventory.json',{'schema':'local2662-fixed-private-input-inventory/v1','artifacts':artifacts,'raw_inputs_not_public':True,'coordinator_log_unchanged_through_projection':True})
result=js('diagnostic-result.json');firstloss=next(row for row in cuts if row['messages']['losses']);assert result['launcher_exit_status']==8 and result['final_report_present'] is False
write('safe-summary.json',{'schema':'local2662-three-trace-diagnostic-summary/v1','source_revision':approval['candidate'],'source_tree':approval['tree'],'supervisor_started_at_utc':result['started_at_utc'],'first_grant_at':start['started_at'],'terminal_cut_at':cuts[-1]['at'],'launcher_exit_status':8,'final_report_present':False,'original_final_verdict':None,'first_cut':cuts[0],'first_loss_cut':firstloss,'terminal_cut':cuts[-1],'finalization':finals[0],'trace_transfer_complete_nodes':3,'trace_total_bytes':sum(row['bytes'] for row in traces),'trace_format_valid':None,'trace_decoded':False,'cleanup_complete':True,'runtime_seconds':cleanup['elapsed_seconds'],'config_sha256':actual['original-distribution-local.yaml'],'configured_SEND_per_second':500,'target_measured_seconds':120,'limits_unchanged':True,'no_rerun':True,'no_DB_or_new_service_or_HTTP_after_stop':True,'safe_projection_preparation_limitation':'Initial extractor expected a hexadecimal string but retained JSON encodes fingerprint as16byte integer array. First script preserved separately; corrected only this safe projection and omitted an unnecessary per-worker classification field (a string, initially assumed numeric), original inputs/run unchanged. Existing two safe outputs reused only after exact-byte equality.','limitations':['native shared Macarm64 diagnostic, not Linux/formal acceptance','simultaneous execution trace adds scheduling/CPU/I/O overhead','first failure stopped original 120-second target early','frozen product_failure/message_loss observation precedes failed finalization; missing final report preserved','trace transfer completion does not establish decodable records or full server interval','runtime trace cannot by itself establish exact cross-node per-RPC identity or root cause']})
print(json.dumps({'safe_payloads_written':11,'trace_manifest':str(R/'safe-trace-manifest.json'),'total_trace_bytes':sum(row['bytes'] for row in traces),'input_immutable':True,'process_and_ports_zero':True}))
