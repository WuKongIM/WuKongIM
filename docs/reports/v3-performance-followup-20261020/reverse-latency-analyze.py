import json, hashlib, statistics
from pathlib import Path
out=Path('/tmp/wukongim-performance-followup-20261020')
paths={'baseline':out/'reverse/report.json','candidate':out/'reverse/comparison/report.json'}
result={}
for name,path in paths.items():
 raw=path.read_bytes(); d=json.loads(raw)
 r={'path':str(path),'sha256':hashlib.sha256(raw).hexdigest(),'product_sha':d['product_sha'],'harness_sha':d['harness_sha'],'binary_sha256':d['binary_sha256'],'windows':[]}
 for wi,w in enumerate(d['windows'],1):
  wr={'window':wi,'start':w['start'],'end':w['end'],'phases':[]}
  for ph in w['phases']:
   t=ph['arrival_timeline']; slow=ph.get('slow_arrivals',[])
   row={k:ph.get(k) for k in ['case','driver_workers','queue_capacity','scheduled','completed','completed_in_window','errors','unexpected_errors','dropped','actual_qps','driver_wait_p99_ms','request_p99_ms','p50_ms','p95_ms','p99_ms','verdict']}
   row['late_completions']=ph['completed']-ph['completed_in_window']; row['dropped_fraction']=ph['dropped']/ph['scheduled']
   row['timeline_summary']={'drop_seconds_zero_indexed':[i for i,v in enumerate(t) if v['dropped']],'over_500ms_seconds_zero_indexed':[i for i,v in enumerate(t) if v['max_latency_ms']>500],'over_200ms_seconds_zero_indexed':[i for i,v in enumerate(t) if v['max_latency_ms']>200],**{k:max(v[k] for v in t) for k in ['max_wait_ms','max_request_ms','max_latency_ms']}}
   row['ten_second_groups']=[]
   for begin in range(0,60,10):
    group=t[begin:begin+10]
    row['ten_second_groups'].append({'start_second':begin,'scheduled_seconds':10,'completed':sum(v['completed'] for v in group),'dropped':sum(v['dropped'] for v in group),**{'median_'+k:statistics.median(v[k] for v in group) for k in ['max_wait_ms','max_request_ms','max_latency_ms']}})
   row['slow_arrivals']=slow
   if slow:row['slow_sample_summary']={'count':len(slow),'min_index':min(x['index'] for x in slow),'max_index':max(x['index'] for x in slow),'average_individual_wait_fraction':statistics.mean(x['wait_ms']/(x['wait_ms']+x['request_ms']) for x in slow)}
   wr['phases'].append(row)
  r['windows'].append(wr)
 r['profile_window_start']=d['profile_window']['start'];r['profile_window_end']=d['profile_window']['end']
 result[name]=r
medians={}
for endpoint in ['/conversation/list','/conversation/sync']:
 m={}
 for name in ['baseline','candidate']:
  ps=[ph for w in result[name]['windows'] for ph in w['phases'] if ph['case']['endpoint']==endpoint]
  m[name]={k:statistics.median(ph[k] for ph in ps) for k in ['driver_wait_p99_ms','request_p99_ms','p50_ms','p95_ms','p99_ms']}
 m['candidate_minus_baseline']={k:m['candidate'][k]-m['baseline'][k] for k in m['candidate']}
 m['candidate_percent_difference']={k:100*(m['candidate'][k]/m['baseline'][k]-1) for k in m['candidate']}
 medians[endpoint]=m
result['matched_medians']=medians
(out/'reverse-latency-data.json').write_text(json.dumps(result,indent=2)+'\n')
for endpoint,m in medians.items(): print(endpoint,json.dumps(m,indent=2))
