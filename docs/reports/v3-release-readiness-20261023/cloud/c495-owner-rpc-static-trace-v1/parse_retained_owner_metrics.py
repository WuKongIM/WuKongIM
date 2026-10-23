from pathlib import Path
import json,re,hashlib,decimal
root=Path('/tmp/wukongim-v3-readiness-20261023/cloud/full-original-distribution-c495-rate500-profile-v1/evidence');out=Path('/tmp/wukongim-v3-readiness-20261023/cloud/c495-owner-rpc-static-trace-v1/retained-owner-metrics.json')
svc='presence owner action';series_re=re.compile(r'^(\w+)(?:\{(.*)\})? ([^ ]+)(?: .*)?$');label_re=re.compile(r'(\w+)="((?:\\.|[^"\\])*)"')
selected={'wukongim_runtime_pool_admission_total','wukongim_runtime_pool_inflight','wukongim_runtime_pool_workers','wukongim_runtime_pool_queue_depth','wukongim_runtime_pool_queue_capacity','wukongim_runtime_pool_queue_bytes','wukongim_runtime_pool_queue_bytes_capacity','wukongim_runtime_pool_task_duration_seconds_count','wukongim_runtime_pool_task_duration_seconds_sum','wukongim_runtime_pool_task_duration_seconds_bucket','wukongim_runtime_pool_wait_duration_seconds_count','wukongim_runtime_pool_wait_duration_seconds_sum','wukongim_runtime_pool_wait_duration_seconds_bucket','wukongim_transport_rpc_total','wukongim_transport_rpc_duration_seconds_count','wukongim_transport_rpc_duration_seconds_sum','wukongim_transport_rpc_duration_seconds_bucket','wukongim_transport_rpc_client_total','wukongim_transport_rpc_client_duration_seconds_count','wukongim_transport_rpc_client_duration_seconds_sum','wukongim_transport_rpc_client_duration_seconds_bucket','wukongim_transport_observer_dropped_total'}
small_buckets={'0.0005','0.001','0.005','0.025','0.05','0.1','0.25','0.5','1','+Inf'}
rows=[];receipts=[]
for cut in ['first-measured','terminal']:
 receipt=json.loads((root/('metrics-'+cut+'-receipt.json')).read_text())
 for n in [1,2,3]:
  p=root/('metrics-'+cut+'-node'+str(n)+'.prom');before=p.stat();b=p.read_bytes();v=next(x for x in receipt if x['node']==n);assert v['complete'] and v['bytes']==len(b) and v['sha256']==hashlib.sha256(b).hexdigest();got=[]
  for l in b.decode().splitlines():
   m=series_re.match(l)
   if not m or m[1] not in selected:continue
   labels={k:json.loads('"'+v+'"') for k,v in label_re.findall(m[2] or '')}
   if m[1]!='wukongim_transport_observer_dropped_total' and svc not in [labels.get('service'),labels.get('task'),labels.get('pool'),labels.get('queue')]:continue
   if m[1].endswith('_bucket') and labels.get('le') not in small_buckets:continue
   labels={k:v for k,v in labels.items() if k not in ['node_name','node_id']};assert set(labels)<={'service','target_node','result','component','pool','priority','queue','task','le'}
   num=decimal.Decimal(m[3]);assert num.is_finite();got.append({'family':m[1],'labels':labels,'value':m[3]})
  after=p.stat();assert before.st_size==after.st_size and before.st_mtime_ns==after.st_mtime_ns
  receipts.append({'cut':cut,'node':n,'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest(),'mtime_ns':before.st_mtime_ns,'unchanged_after_read':True});rows.append({'cut':cut,'node':n,'series':got})
# Exact counter observations remain strings, with missing series explicit in consumer queries.
v={'source':'six already retained Prometheus text files only','receipts':receipts,'selected_series':rows,'limits':['Cumulative cuts are not a per-request timeline; file mtimes are retention metadata, not instant sampling timestamps.','Owner service mixes operation9/10 reads with route actions; totals do not distinguish those operations.','Handler and client latencies are sampled one in32; absence of slow sampled handler does not bound all handler durations.','The first/terminal gauges do not measure peak/intermediate occupancy.','Service task result ok is transport-handler success; typed application rejection encoded in a successful reply remains ok.','Response callback ignores Send enqueue error; no dedicated response-Send-result metric or retained request correlation exists.','No other labels, raw metrics, UIDs, payloads or runtime error text are copied.']};out.write_text(json.dumps(v,indent=2)+'\n')
for row in rows:
 def pick(f):return [x for x in row['series'] if x['family']==f]
 def histmax(f):
  samples=pick(f+'_count');results=[]
  for c in samples:
   buckets=[x for x in pick(f+'_bucket') if all(x['labels'].get(k)==z for k,z in c['labels'].items())]
   count=decimal.Decimal(c['value']);full=[x['labels']['le'] for x in buckets if decimal.Decimal(x['value'])==count and x['labels']['le']!='+Inf'];results.append({'labels':c['labels'],'sample_count':c['value'],'highest_sample_upper_bound_s':min(map(decimal.Decimal,full)) if full else None})
  return results
 print(json.dumps({'node':row['node'],'cut':row['cut'],'admission':pick('wukongim_runtime_pool_admission_total'),'handlers':pick('wukongim_transport_rpc_total'),'clients':pick('wukongim_transport_rpc_client_total'),'handler_sample_bounds':histmax('wukongim_transport_rpc_duration_seconds'),'queue_wait_sample_bounds':histmax('wukongim_runtime_pool_wait_duration_seconds'),'gauges':[x for x in row['series'] if x['family'] in ['wukongim_runtime_pool_queue_depth','wukongim_runtime_pool_inflight','wukongim_transport_observer_dropped_total']]},default=str))
