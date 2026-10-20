"""Read exact retained fixed-host reports; never infer a release gate or root cause."""
import argparse
import hashlib
import json
import math
import statistics
from pathlib import Path

p=argparse.ArgumentParser();p.add_argument('artifact_root',type=Path);p.add_argument('--output',type=Path,required=True);args=p.parse_args()
sequence=['B','B','A','B','B','A','C']
endpoints=['/conversation/list','/conversation/sync']
blocks=[]
for index,variant in enumerate(sequence,1):
 path=args.artifact_root/'blocks'/f'{index:02}-{variant}'/'report.json'
 raw=path.read_bytes();assert len(raw)<=12*1024*1024
 report=json.loads(raw)
 assert report['complete'] and len(report['windows'])==3
 rows=[]
 for ordinal,window in enumerate(report['windows'],1):
  phases=window['phases'];assert len(phases)==2
  assert sorted(ph['case']['endpoint'] for ph in phases)==sorted(endpoints)
  rows.append({'window':ordinal,'start':window['start'],'end':window['end'],'cpu_seconds':window['cpu_seconds'],'allocated_bytes':window['allocated_bytes'],'completed':sum(ph['completed'] for ph in phases),'runtime_loads':window['runtime_loads'],'membership_writes':window['membership_writes'],'phases':[{k:ph[k] for k in ['case','completed','completed_in_window','actual_qps','p99_ms','request_p99_ms','driver_wait_p99_ms','errors','unexpected_errors','dropped','verdict']} for ph in phases]})
 total=sum(w['completed'] for w in rows);assert total>0
 summary={}
 for endpoint in endpoints:
  phases=[ph for w in rows for ph in w['phases'] if ph['case']['endpoint']==endpoint]
  summary[endpoint]={'p99_window_median_ms':statistics.median(ph['p99_ms'] for ph in phases),'p99_window_mean_ms':statistics.mean(ph['p99_ms'] for ph in phases),'qps_window_median':statistics.median(ph['actual_qps'] for ph in phases),'window_p99_ms':[ph['p99_ms'] for ph in phases],'window_verdicts':[ph['verdict'] for ph in phases]}
 healthy=all(ph['verdict']=='pass' and ph['errors']==ph['unexpected_errors']==ph['dropped']==0 for w in rows for ph in w['phases']) and all(w['runtime_loads']==w['membership_writes']==0 for w in rows)
 blocks.append({'index':index,'variant':variant,'report_path':str(path),'report_sha256':hashlib.sha256(raw).hexdigest(),'identity':{k:report[k] for k in ['product_sha','binary_sha256','harness_sha','harness_dirty','profile_sha256','driver_gomaxprocs','node_gomaxprocs']},'healthy_windows':healthy,'completed':total,'server_cpu_ms_per_completed':1000*sum(w['cpu_seconds'] for w in rows)/total,'allocated_bytes_per_completed':sum(w['allocated_bytes'] for w in rows)/total,'summary':summary,'windows':rows})
for key in ['harness_sha','profile_sha256','driver_gomaxprocs','node_gomaxprocs']:
 assert len({b['identity'][key] for b in blocks})==1,key
assert not any(b['identity']['harness_dirty'] for b in blocks)
for variant in ['A','B','C']:
 for key in ['product_sha','binary_sha256']:
  assert len({b['identity'][key] for b in blocks if b['variant']==variant})==1,(variant,key)
def difference(a,b):
 assert a>0 and b>0 and math.isfinite(a+b)
 return abs(a-b)/min(a,b)
aa=[]
for left,right in [(1,2),(4,5)]:
 a,b=blocks[left-1],blocks[right-1]
 cpu=difference(a['server_cpu_ms_per_completed'],b['server_cpu_ms_per_completed'])
 q={endpoint:difference(a['summary'][endpoint]['p99_window_median_ms'],b['summary'][endpoint]['p99_window_median_ms']) for endpoint in endpoints}
 aa.append({'blocks':[left,right],'server_cpu_per_request_relative_difference':cpu,'endpoint_median_p99_relative_differences':q,'diagnostic_repeatability_pass':a['healthy_windows'] and b['healthy_windows'] and cpu<=0.10 and all(v<=0.25 for v in q.values())})
spread={}
for endpoint in endpoints:
 bvals=[b['summary'][endpoint]['p99_window_median_ms'] for b in blocks if b['variant']=='B']
 avals=[b['summary'][endpoint]['p99_window_median_ms'] for b in blocks if b['variant']=='A']
 bb=[b['summary'][endpoint]['p99_window_median_ms'] for b in blocks if b['index'] in [4,5]]
 spread[endpoint]={'B_all_four_block_medians_ms':bvals,'B_max_over_min':max(bvals)/min(bvals),'A_both_block_medians_ms':avals,'A_max_over_min':max(avals)/min(avals),'ABBA_B_median_of_block_medians_ms':statistics.median(bb),'ABBA_A_median_of_block_medians_ms':statistics.median(avals),'ABBA_B_relative_difference_from_A':statistics.median(bb)/statistics.median(avals)-1,'limits':'Descriptive block/window summaries only, not pooled P99 or statistical significance. Context/prepare/route validity still needs independent review.'}
result={'schema':'wukongim/fixed-host-independent-analysis/v1','blocks':blocks,'aa':aa,'cross_time_and_abba':spread,'all_aa_criteria_pass':all(a['diagnostic_repeatability_pass'] for a in aa),'C_mixed_all_pass':blocks[-1]['healthy_windows'],'full_release_matrix_verified_here':False,'historical_root_cause_confirmed_here':False}
args.output.write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:result[k] for k in ['all_aa_criteria_pass','C_mixed_all_pass','full_release_matrix_verified_here','historical_root_cause_confirmed_here']}))
