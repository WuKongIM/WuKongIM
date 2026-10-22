from pathlib import Path
import json,re,collections,hashlib,gzip,datetime,subprocess
P=Path('/tmp/wukongim-v3-readiness-20261023/cloud/full-original-distribution-profile-node2-v1')
O=P.parent/'profile-node2-signals-v1'
S=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis')
x=json.loads((O/'limited-summary.json').read_text())
def sha(b):return hashlib.sha256(b).hexdigest()
def parse(q):
 out=[]
 for line in q.read_text().splitlines():
  if not line or line.startswith('#'):continue
  m=re.fullmatch(r'([a-zA-Z_:][a-zA-Z0-9_:]*)(\{.*\})?\s+([^\s]+)(?:\s+.*)?',line)
  if not m:continue
  name,labels,value=m.groups()
  if not any(name.startswith(prefix) for prefix in ['wukongim_gateway_async_send_batch_records','wukongim_message_permission_counts_total','wukongim_transport_lane_payload_bytes_total','go_sched_gomaxprocs_threads','go_info']):continue
  safe={k:v for k,v in re.findall(r'([a-zA-Z_][a-zA-Z0-9_]*)="([^"\\]*)"',labels or '') if k in ['kind','le','version','direction','priority']}
  out.append({'name':name,'labels':safe,'value':float(value)})
 return out
x['actual_batch_and_runtime_cuts']=[]
for n in [1,2,3]:
 for cut in ['first-measured','terminal']:
  rows=parse(P/f'evidence/metrics-{cut}-node{n}.prom');record={'node':n,'cut':cut,'runtime_and_transport_fixed_signals':rows}
  hist={r['name'].removeprefix('wukongim_gateway_async_send_batch_records'):r['value'] for r in rows if r['name'] in ['wukongim_gateway_async_send_batch_records_sum','wukongim_gateway_async_send_batch_records_count']}
  hist['le_1']=next(r['value'] for r in rows if r['name']=='wukongim_gateway_async_send_batch_records_bucket' and r['labels']['le']=='1')
  record['gateway_batch_histogram_scalar']=hist
  record['actual_mean_batch_records']=hist['_sum']/hist['_count'] if hist['_count'] else None
  x['actual_batch_and_runtime_cuts'].append(record)
# Bounded protobuf top-level header only: no samples, strings, stacks or labels
# are decoded or copied. Field numbers follow profile/encode.go at the pinned
# github.com/google/pprof module; its source hash is retained below.
raw=(P/'profiles-node2/profiles/node-2-cpu.pb.gz').read_bytes();assert len(raw)==40474 and sha(raw)=='d3c5e46713de6add18641f2f4f4a0672cbac6db16322d555cdb0236cf32dcd23'
buf=gzip.decompress(raw);assert len(buf)<4*1024*1024
at=0;header={}
def varint():
 global at
 value=0;shift=0
 while True:
  b=buf[at];at+=1;value|=(b&127)<<shift
  if b<128:return value
  shift+=7;assert shift<70
while at<len(buf):
 tag=varint();field=tag>>3;wire=tag&7
 if wire==0:
  v=varint()
  if field in [9,10,12]:header[field]=v
 elif wire==1:at+=8
 elif wire==2:
  size=varint();at+=size
 elif wire==5:at+=4
 else:raise ValueError('unsupported top-level wire type')
 assert at<=len(buf)
def stamp(ns):
 sec,nano=divmod(ns,10**9)
 return datetime.datetime.fromtimestamp(sec,datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S')+'.'+str(nano).zfill(9)+'Z'
profile={'cpu_sha256':sha(raw),'cpu_bytes':len(raw),'header_start_unix_nanos':str(header[9]),'header_duration_nanos':str(header[10]),'cpu_period_nanos':str(header[12]),'header_start_utc':stamp(header[9]),'header_end_utc':stamp(header[9]+header[10]),'top_total_sample_seconds':4.34,'top_rounded_duration_seconds':10.17,'top_sample_share_of_one_CPU_percent':42.68,'cpu_share_not_host_capacity':True,'goroutine_capture':'missing','heap_capture':'not_requested','capture_group_status':'partial/valid=false/profile_capture_missing','parent_helper_launch_utc':'2026-10-22T20:09:30.295512+00:00','actual_goroutine_request_offset_seconds':None,'active_SEND_end_boundary':None,'profile_may_include_drain':True,'not_valid_for_performance_pairing':True}
codec=Path('/Users/tt/go/pkg/mod/github.com/google/pprof@v0.0.0-20260709232956-b9395ee17fa0/profile/encode.go')
profile['header_schema_source']={'path':str(codec),'sha256':sha(codec.read_bytes()),'encoding_lines':'152-153 (time_nanos=9, duration_nanos=10); period=12'}
x['cpu_profile']=profile
x['binary_provenance']={'correction_directory':'provenance-addendum','public_manifest_sha256':'a2d430dc95d71b8a819fa342863860f62f929ea11a9479c6d17c4a9b5bc60d9e','binary_embedded_revision':'a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83','binary_embedded_go':'go1.25.11','declared_analysis_revision':'79a7177032a6ae1ec7015cefe0e6f94eb0f19f06','not_a_verified_79a_candidate_reproduction':True,'actual_compilation_source_inferred_from_stamp':False,'actual_compilation_source_verified_by_this_axis':False,'stamp_semantics_clarification_manifest_sha256':'9414f04c8422106f0e95f6654cf1c37d640a50415a2853a447cf338ee631aa4a'}
x['missing_observations']+=['fault-time recovery lane wait/hold histograms and cold-page counts','which admission constraint triggered each permission refusal','per-SENDACK reason/identity alignment for server189 vs client185 rejection counts','cross-node CPU profiles and blocked/mutex/trace evidence','cold-vs-warm controlled presence comparison']
x['aggregate_rpc_timeout_counters']=dict(collections.Counter({}))
t=collections.Counter();rpc=collections.defaultdict(lambda:[0,0])
for c in x['metric_cuts']:
 if c['cut']=='terminal':
  for r in c['fixed_counters_and_pressure']:
   if r['name']=='wukongim_transport_rpc_client_total' and r['labels'].get('result')=='timeout':t[r['labels']['service']]+=r['value']
for r in x['snapshot_delta_means']:
 if r['name']=='wukongim_transport_rpc_client_duration_seconds':
  key=r['labels']['service'];rpc[key][0]+=r['delta_count'];rpc[key][1]+=r['delta_total_seconds']
x['aggregate_rpc_timeout_counters']=dict(t)
x['between_preserved_cut_RPC_sample_means']={k:{'count':c,'mean_ms':s/c*1000} for k,(c,s) in rpc.items()}
x['provenance_limits_precede_hypotheses']=True
(O/'limited-summary.json').write_text(json.dumps(x,indent=2)+'\n')
paths=['report/final.json','report/run-start.json','report/worker-evidence.json','report/diagnostic-status.json','diagnostic-cuts.jsonl','diagnostic-result.json','host-process-samples.jsonl','preflight.json','cleanup-receipt.json','supervisor-events.json','evidence/first-measured-status.json','evidence/metrics-first-measured-receipt.json','evidence/metrics-terminal-receipt.json','profiles-node2/metadata.json','profiles-node2/profiles/node-2-cpu.pb.gz','profile-driver-status.json','pprof-offline-receipt.json','safe-cpu-top.txt','safe-cpu-cum.txt','logs/coordinator.log']+[f'evidence/metrics-{cut}-node{n}.prom' for n in [1,2,3] for cut in ['first-measured','terminal']]+[f'logs/service-{n}.log' for n in [1,2,3]]
bindings=[{'path':str(P/q),'bytes':(P/q).stat().st_size,'sha256':sha((P/q).read_bytes())} for q in paths]
for cut in ['first-measured','terminal']:
 for receipt in json.loads((P/f'evidence/metrics-{cut}-receipt.json').read_text()):
  q=P/f"evidence/metrics-{cut}-node{receipt['node']}.prom";assert receipt['complete'] and q.stat().st_size==receipt['bytes'] and sha(q.read_bytes())==receipt['sha256']
receipt=json.loads((P/'pprof-offline-receipt.json').read_text());assert receipt['cpu_sha256']==sha(raw)
for row in receipt['commands']:
 assert row['exit_status']==0 and sha((P/row['safe_file']).read_bytes())==row['safe_sha256']
for name in ['safe-cpu-top.txt','safe-cpu-cum.txt']:(O/name).write_bytes((P/name).read_bytes())
source=json.loads((O/'provenance-addendum/binding.json').read_text())
(O/'source-input-binding.json').write_text(json.dumps({'source_revision_statement':'analysis 79a; executables embedded a804; see correction addendum before interpreting','source_files':source['analysis_files_verified_byte_identical'],'finite_input_files':bindings,'metrics_receipts_verified':True,'cpu_and_derived_top_receipts_verified':True,'pprof_raw_publicly_copied':False,'raw_UID_channel_payload_config_logs_copied':False,'product_or_network_or_cloud_started':False,'cleanup_receipt_complete':json.loads((P/'cleanup-receipt.json').read_text())['complete'],'no_test_or_fix_claim':'Bounded read-only evidence route only, no new reproduction or repair attempted.'},indent=2)+'\n')
print('profile-header',profile['header_start_utc'],profile['header_end_utc'],'finite inputs',len(bindings))
