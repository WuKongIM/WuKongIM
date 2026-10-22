from pathlib import Path
import json,re,collections,datetime,hashlib,subprocess
P=Path('/tmp/wukongim-v3-readiness-20261023/cloud/full-original-distribution-profile-node2-v1')
O=P.parent/'profile-node2-signals-v1'
S=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis')
f=json.loads((P/'report/final.json').read_text());seconds=f['window']['elapsed']/1e9
start=datetime.datetime.fromisoformat(f['window']['start']).timestamp();end=datetime.datetime.fromisoformat(f['window']['end']).timestamp()
LABELS={'node_id','node_name','component','pool','priority','queue','result','kind','stage','class','reason','source','outcome','path','stale_retry','target_node','service','service_id','lane','direction','op','le','scope','operation','phase','task','role','unit','mode','status'}
def parse(lines):
 out=[]
 for line in lines:
  if not line or line.startswith('#'):continue
  m=re.fullmatch(r'([a-zA-Z_:][a-zA-Z0-9_:]*)(\{.*\})?\s+([^\s]+)(?:\s+.*)?',line)
  if not m:continue
  name,labels,value=m.groups();safe={k:json.loads('"'+v+'"') for k,v in re.findall(r'([a-zA-Z_][a-zA-Z0-9_]*)="((?:\\.|[^"\\])*)"',labels or '') if k in LABELS}
  try:value=float(value)
  except ValueError:continue
  out.append({'name':name,'labels':safe,'value':value})
 return out
metrics={}
for n in [1,2,3]:
 for cut in ['first-measured','terminal']:
  metrics[n,cut]=parse((P/f'evidence/metrics-{cut}-node{n}.prom').read_text().splitlines())
def identity(row):return row['name'],tuple(sorted(row['labels'].items()))
def select(n,cut,predicate):return [r for r in metrics[n,cut] if predicate(r)]
def scalar(n,cut,name):
 r=select(n,cut,lambda r:r['name']==name and not r['labels']);return r[0]['value'] if len(r)==1 else None
summary={'window':f['window'],'rates':{'denominator_seconds':seconds,'source':'final.window.elapsed; includes completion/stop rather than a proven steady arrival interval','target_primary_SEND_per_second':2000,'primary_generated_per_second':f['generated']['primary']/seconds,**{k+'_per_second':f['messages'][k]/seconds for k in ['sent','send_attempts','first_attempts','send_acknowledged','received','retry_attempts']},'first_failure_ratio':f['messages']['first_attempt_failures']/f['messages']['first_attempts']},'final_messages':f['messages'],'correlation':f['correlation'],'harness':f['harness'],'worker_queues':f['queues'],'resources_final_single_observation':f['resources'],'cluster':f['cluster'],'metadata_create':f['meta_create'],'log_summary':[],'metric_cuts':[],'resource_samples':[],'latency':{},'snapshot_delta_means':[]}
for name,x in f['latency'].items():
 if not isinstance(x,dict) or 'count' not in x:continue
 count=x['count'];bounds=x['bucket_upper_nanos'];buckets=x['buckets']
 def q(p):
  if not count:return None
  total=0
  for upper,num in zip(bounds,buckets):
   total+=num
   if total>=count*p:return upper/1e6
 summary['latency'][name]={'count':count,'mean_ms':x['sum_nanos']/count/1e6 if count else None,'max_ms':x['max_nanos']/1e6,'p99_histogram_upper_ms':q(.99),'p999_histogram_upper_ms':q(.999)}
for n in [1,2,3]:
 events=collections.Counter();send=collections.Counter();delivery=collections.Counter();times={'send_failed':[],'delivery':[]};known=collections.Counter();durations=collections.Counter()
 for line in (P/f'logs/service-{n}.log').read_text().splitlines():
  m=re.search(r'\{.*\}',line)
  if not m:continue
  try:d=json.loads(m[0])
  except:continue
  event=d.get('event','absent');events[event]+=1
  if event=='internal.access.gateway.send_failed':
   send[(d.get('sourceModule'),d.get('source'),d.get('errorClass'))]+=1;times['send_failed'].append(line[:23])
   for phrase in ['permission read admission busy','internal/message: route not ready','internal/message: backpressured']:
    known[phrase]+=phrase in str(d.get('error',''))
   durations.update(k for k in d if k in ['failedStage','permissionDuration','preAppendDuration','submitterDuration','deadlineBudgetBeforeSubmit'])
  if event=='internal.app.delivery.plan_incomplete':
   delivery[(d.get('phase'),d.get('result'),d.get('mode'))]+=1;times['delivery'].append(line[:23])
 summary['log_summary'].append({'node':n,'event_counts':dict(events),'send_failures':[{'source_module':a,'source':b,'class':c,'count':count} for (a,b,c),count in send.items()],'delivery_failures':[{'phase':a,'result':b,'mode':c,'count':count} for (a,b,c),count in delivery.items()],'fixed_error_sentinel_text_matches':dict(known),'send_duration_field_presence':dict(durations),'first_last_at':{k:{'first':v[0] if v else None,'last':v[-1] if v else None} for k,v in times.items()}})
 for cut in ['first-measured','terminal']:
  rows=metrics[n,cut];idx={identity(r):r for r in rows};queues=[]
  for r in rows:
   if r['name'] not in ['wukongim_runtime_pool_queue_depth','wukongim_channel_worker_queue_depth','wukongim_channelv2_worker_queue_depth','wukongim_delivery_recipient_worker_queue_depth']:continue
   capacity=idx.get((r['name'].replace('_depth','_capacity'),tuple(sorted(r['labels'].items()))))
   if capacity and capacity['value']>0:queues.append({'family':r['name'],'labels':r['labels'],'depth':r['value'],'capacity':capacity['value'],'percent':100*r['value']/capacity['value']})
  counters=select(n,cut,lambda r:r['name'] in ['wukongim_gateway_sendacks_total','wukongim_message_permission_counts_total','wukongim_message_permission_cohort_owned','wukongim_message_permission_inflight','wukongim_delivery_recipient_worker_process_total','wukongim_delivery_recipient_worker_inflight','wukongim_delivery_recipient_worker_capacity','wukongim_delivery_push_rpc_total','wukongim_presence_endpoint_lookup_total','wukongim_channelappend_local_admission_total','wukongim_channelappend_effect_total','wukongim_channelappend_router_total','wukongim_message_append_total','wukongim_transport_rpc_client_total','wukongim_transport_rpc_total'])
  summary['metric_cuts'].append({'node':n,'cut':cut,'sample_timestamp':'not individually retained; bounded sequential scrape after supervisor event','standard_process_family_count':sum(r['name'].startswith('process_') for r in rows),'go_metrics':{name:scalar(n,cut,name) for name in ['go_goroutines','go_memstats_heap_alloc_bytes','go_memstats_heap_sys_bytes','go_gc_duration_seconds_count','go_gc_duration_seconds_sum','go_sched_latencies_seconds_count','go_sched_latencies_seconds_sum']},'runtime_queue_max_percent':max([q['percent'] for q in queues if q['family']=='wukongim_runtime_pool_queue_depth'] or [0]),'queues_above_50_percent':[q for q in queues if q['percent']>=50],'fixed_counters_and_pressure':counters})
 first={identity(r):r for r in metrics[n,'first-measured']};terminal={identity(r):r for r in metrics[n,'terminal']}
 for key,row in terminal.items():
  if not row['name'].endswith('_count') or not any(fragment in row['name'] for fragment in ['message_permission_duration_seconds','delivery_push_rpc_duration_seconds','delivery_recipient_worker_process_duration_seconds','presence_endpoint_lookup_duration_seconds','transport_rpc']):continue
  sumkey=(row['name'][:-6]+'_sum',key[1]);a=first.get(key);sa=first.get(sumkey);sb=terminal.get(sumkey)
  if not all(x is not None for x in [a,sa,sb]):continue
  count=row['value']-a['value'];total=sb['value']-sa['value']
  if count>0 and total>=0:summary['snapshot_delta_means'].append({'node':n,'name':row['name'][:-6],'labels':row['labels'],'delta_count':count,'delta_total_seconds':total,'delta_mean_ms':total/count*1000,'window_note':'between two asynchronous preserved cuts; not full measured window or per-failure duration'})
for line in (P/'host-process-samples.jsonl').read_text().splitlines():
 r=json.loads(line);stamp=datetime.datetime.fromisoformat(r['at_utc']).timestamp()
 if not start<=stamp<=end:continue
 summary['resource_samples'].append({'at_utc':r['at_utc'],'aggregate_ps_cpu_percent':r['aggregate_process_cpu_percent'],'other_wukongim_process_count':len(r['other_wukongim_processes']),'owned_product_or_cli':[x for x in r['owned'] if x['process'] in ['wukongim','wkcli']],'note':'ps pcpu read directly; not an independently measured interval CPU or host capacity fraction'})
(O/'limited-summary.json').write_text(json.dumps(summary,indent=2)+'\n')

initial=json.loads((P/'evidence/first-measured-status.json').read_text())
wall_initial=(datetime.datetime.fromisoformat(initial['updated_at'])-datetime.datetime.fromisoformat(initial['started_at'])).total_seconds()
initial_messages=[]
for line in (P/'logs/coordinator.log').read_text().splitlines():
 match=re.search(r'\{.*\}',line)
 if not match:continue
 document=json.loads(match[0])
 if document.get('cut')=='periodic':initial_messages.append(document['messages'])
if len(initial_messages)==1:
 summary['initial_periodic_cut']={'at':initial['updated_at'],'wall_elapsed_seconds':wall_initial,'source':'wall timestamps, not a separate monotonic duration','messages':initial_messages[0],'approx_sent_per_second':initial_messages[0]['sent']/wall_initial,'approx_ack_per_second':initial_messages[0]['send_acknowledged']/wall_initial}
summary['metric_cut_supervisor_events']=json.loads((P/'supervisor-events.json').read_text())
summary['host_preflight']=json.loads((P/'preflight.json').read_text())
summary['missing_observations']=['standard process_* at every retained Mac product scrape','per-failed-SENDACK numeric packet reason and attempt duration','winning pool identity in final service_queue_percent aggregate','precise per-node snapshot timestamp','worker Go scheduler/GC profiles and standard process_* interval counters','independent interval CPU/cgroup/steal per four separate hosts','fault-time goroutine/mutex/block profiles; CPU only on node2','causal join between permission rejection, delivery timeouts and expired2']
(O/'limited-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
print('summary retained:',len(summary['metric_cuts']),'metric cuts,',len(summary['resource_samples']),'host points,',len(summary['snapshot_delta_means']),'bounded duration groups')
