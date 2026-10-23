"""One numeric parse of six fixed immutable captures; no raw label/error projection."""
from pathlib import Path
import collections,decimal,hashlib,json,os,re,stat
D=decimal.Decimal
R=Path('/tmp/wukongim-v3-readiness-20261023/cloud/full-original-distribution-2662-rate500-three-trace-v1/evidence')
O=Path('/tmp/wukongim-v3-readiness-20261023/cloud/2662-transport-two-cut-metrics-corrected-v2')
SERVICE={'presence authority','presence owner action'};PRIORITY={'raft','control','rpc','bulk'};RESULT={'ok','err','timeout','canceled','full','stopped','invalid','panic','busy','closed','too_large','dropped','coalesced','dirty','requeued','other'}
RPC={'wukongim_transport_rpc_client_total','wukongim_transport_rpc_client_duration_seconds_bucket','wukongim_transport_rpc_client_duration_seconds_count','wukongim_transport_rpc_client_duration_seconds_sum','wukongim_transport_rpc_total','wukongim_transport_rpc_duration_seconds_bucket','wukongim_transport_rpc_duration_seconds_count','wukongim_transport_rpc_duration_seconds_sum'}
PRESSURE={'wukongim_runtime_pool_admission_total','wukongim_runtime_pool_wait_duration_seconds_bucket','wukongim_runtime_pool_wait_duration_seconds_count','wukongim_runtime_pool_wait_duration_seconds_sum','wukongim_runtime_pool_queue_depth','wukongim_runtime_pool_queue_capacity','wukongim_runtime_pool_queue_bytes','wukongim_runtime_pool_queue_bytes_capacity'}
RESOURCE={'process_start_time_seconds','process_cpu_seconds_total','process_resident_memory_bytes','go_sched_gomaxprocs_threads','go_gomaxprocs','go_goroutines','go_threads','wukongim_node_cpu_percent','wukongim_node_memory_rss_bytes','node_memory_MemAvailable_bytes','node_memory_available_bytes','wukongim_host_memory_available_bytes'}
LABELS=('target_node','service','result','component','pool','queue','priority','le')
LINE=re.compile(r'^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)\})?\s+([^\s]+)(?:\s+[^\s]+)?$')
LAB=re.compile(r'([a-zA-Z_][a-zA-Z0-9_]*)="((?:\\.|[^"\\])*)"')
def number(x):return int(x) if x==x.to_integral_value() else float(x)
def put(name,data):
 raw=(json.dumps(data,indent=2)+'\n').encode();assert len(raw)<=1048576
 fd=os.open(O/name,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
 with os.fdopen(fd,'wb') as f:f.write(raw)
def identity(s):return {'bytes':s.st_size,'mtime_ns':s.st_mtime_ns,'device':s.st_dev,'inode':s.st_ino}
def permitted(name,l,node):
 if l.get('node_id',str(node))!=str(node):return False
 if name in RPC:return l.get('service') in SERVICE and l.get('target_node','1') in {'1','2','3'} and l.get('result','ok') in RESULT
 if name in PRESSURE:
  return l.get('component')=='transport' and l.get('result','ok') in RESULT and ((l.get('pool')=='scheduler' and l.get('queue')=='scheduler' and l.get('priority') in PRIORITY) or (l.get('pool')=='service' and l.get('queue') in SERVICE and l.get('priority') in PRIORITY|{'none'}))
 return name in RESOURCE or name=='wukongim_transport_observer_dropped_total'
series={};pins=[];rejected=collections.Counter()
EXPECTED={r['path']:r for r in json.loads(Path('/tmp/wukongim-v3-readiness-20261023/cloud/2662-transport-two-cut-metrics-v1/inputs-before-after.json').read_text())['inputs']}
for cut in ['first-measured','terminal']:
 for node in [1,2,3]:
  p=R/f'metrics-{cut}-node{node}.prom';s=p.stat();assert p.is_file() and not p.is_symlink() and 0<s.st_size<=2097152
  assert identity(s)=={k:EXPECTED[str(p)][k] for k in ['bytes','mtime_ns','device','inode']}
  h=hashlib.sha256();data={};linecount=0
  with p.open('rb') as f:
   for raw in f:
    h.update(raw);linecount+=1
    if raw.startswith(b'#'):continue
    text=raw.decode('utf-8').strip();name=text.split('{',1)[0].split(' ',1)[0]
    if name not in RPC|PRESSURE|RESOURCE|{'wukongim_transport_observer_dropped_total'}:continue
    match=LINE.fullmatch(text)
    if not match:rejected['selected_syntax']+=1;continue
    name,labeltext,value=match.groups();labels={k:json.loads('"'+v+'"') for k,v in LAB.findall(labeltext or '')}
    if not permitted(name,labels,node):rejected['outside_closed_scope']+=1;continue
    try:v=D(value)
    except decimal.InvalidOperation:rejected['nondecimal']+=1;continue
    if not v.is_finite():rejected['nonfinite']+=1;continue
    key=(name,tuple((k,labels[k]) for k in LABELS if k in labels))
    if key in data:raise ValueError('duplicate selected identity')
    data[key]=v
  end=p.stat();assert identity(s)==identity(end) and h.hexdigest()==EXPECTED[str(p)]['sha256']
  series[(cut,node)]=data;pins.append({'cut':cut,'node_id':node,'path':str(p),'sha256':h.hexdigest(),**identity(s),'lines':linecount,'selected_series':len(data)})
# Integrity-only second read; numeric content is not parsed a second time.
for row in pins:
 p=Path(row['path']);h=hashlib.sha256()
 with p.open('rb') as f:
  while chunk:=f.read(1048576):h.update(chunk)
 assert h.hexdigest()==row['sha256'] and identity(p.stat())=={k:row[k] for k in ['bytes','mtime_ns','device','inode']}
def delta(first,last):
 if first is None:return None,'new_series_unknown' if last is not None else 'absent_both'
 if last is None:return None,'disappeared_unknown'
 if last<first:return None,'reset_unknown'
 return last-first,'matched_monotone'
facts=[]
for node in [1,2,3]:
 a=series[('first-measured',node)];b=series[('terminal',node)]
 get=lambda data,name:next((v for (n,ls),v in data.items() if n==name and not ls),None)
 sa,sb=get(a,'process_start_time_seconds'),get(b,'process_start_time_seconds');pidstate='same_process_start' if sa is not None and sa==sb else 'unknown_or_changed_process_start'
 rows=[]
 for key in sorted(set(a)|set(b)):
  name,labels=key;first=a.get(key);last=b.get(key);d,state=delta(first,last)
  gauge=name in RESOURCE and name!='process_cpu_seconds_total' or name.startswith('wukongim_runtime_pool_queue_')
  row={'metric':name,**dict(labels),'first':None if first is None else number(first),'terminal':None if last is None else number(last),'delta':None if gauge or d is None else number(d),'state':'gauge_two_points' if gauge else state}
  rows.append(row)
 counters=[r for r in rows if r['metric']=='wukongim_transport_rpc_client_total']
 fractions=[]
 for svc in ['presence authority','presence owner action']:
  matched=[r for r in counters if r['service']==svc and r['state']=='matched_monotone'];uncertain=[r for r in counters if r['service']==svc and r['state']!='matched_monotone'];num=sum(r['delta'] for r in matched if r.get('result')=='timeout');den=sum(r['delta'] for r in matched)
  fractions.append({'service':svc,'matched_timeout_delta':num,'matched_all_outcome_delta':den,'matched_timeout_fraction':None if not den else num/den,'counter_identity':pidstate,'unmatched_series_count':len(uncertain),'exact_observed_counter_ratio':pidstate=='same_process_start' and not uncertain and den>0,'absent_outcomes_not_inferred_zero':True})
 facts.append({'node_id':node,'process_identity':pidstate,'process_start_first':None if sa is None else number(sa),'process_start_terminal':None if sb is None else number(sb),'client_timeout_fractions':fractions,'series':rows})
put('facts.json',{'schema':'wukongim.transport-fixed-two-cut/v1','numerical_parse_passes':1,'integrity_hash_verification_passes':1,'inputs_unchanged':True,'nodes':facts,'rejected_fixed_scope_counts':dict(rejected),'limits':['Counters are exact published count batches, asynchronously drained; durations are deterministic one-in32 samples per event key, not all calls.','No percentile interpolation or 32x extrapolation. Histogram intervals aggregate target/service; outcomes are not a histogram label.','New/disappeared/reset series remain unknown, not zero. Process-start identity is checked only if present in these same captures.','Each file is a non-atomic scrape/cut; not exactly the earlier trace window, no same-RPC/request/reply association.','Scheduler priority queues mix frame kinds/services/directions; admission/canceled cannot identify a response or particular READ.','Resource gauges are two/latest samples, not peak/headroom; no additional resource probe.']})
put('inputs-before-after.json',{'inputs':pins,'all_six_unchanged':True,'metadata_before_after_equal':True,'sha_before_after_equal':True})
print(json.dumps({'nodes':[{'node_id':n['node_id'],'process_identity':n['process_identity'],'client_fractions':n['client_timeout_fractions'],'observer_dropped':[r for r in n['series'] if r['metric']=='wukongim_transport_observer_dropped_total'],'scheduler_admission':[r for r in n['series'] if r['metric']=='wukongim_runtime_pool_admission_total' and r.get('pool')=='scheduler']} for n in facts],'selected_series':sum(len(n['series']) for n in facts),'input_bytes':sum(r['bytes'] for r in pins)},indent=2))
