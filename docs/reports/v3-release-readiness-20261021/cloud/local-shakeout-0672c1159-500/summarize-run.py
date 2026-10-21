#!/usr/bin/env python3
"""Offline projection of one completed local shakeout; never runs a workload."""
import argparse, collections, datetime, hashlib, json, math, os, re
from pathlib import Path
p=argparse.ArgumentParser(); p.add_argument('--root',type=Path,required=True); p.add_argument('--output',type=Path,required=True)
a=p.parse_args(); root=a.root.resolve()
if a.output.exists():raise SystemExit('refuse to overwrite output')
def read(name):return json.loads((root/name).read_text())
def sha(path):
 h=hashlib.sha256()
 with path.open('rb') as f:
  for b in iter(lambda:f.read(1024*1024),b''):h.update(b)
 return h.hexdigest()
def dt(s):return datetime.datetime.fromisoformat(s.replace('Z','+00:00'))
result=read('result.json'); command=read('command.json'); final=read('run/report/final.json'); preflight=read('preflight.json')
assert command['finished_at'] and final['final'] and final['verdict']['terminal']
assert result['source_unchanged'] and result['cleanup']['pass']
identity=dict(line.split('\t',1) for line in (root/'run/evidence/identity.tsv').read_text().splitlines())
binaries=[]
for name,key in [('wukongim','wukongim_binary_sha256'),('wkcli','wkbench_binary_sha256')]:
 path=root/'run/bin'/name; digest=sha(path); assert digest==identity[key]
 binaries.append({'name':name,'bytes':path.stat().st_size,'sha256':digest,'matches_helper_identity':True})
assert sha(root/'run/chat-lifecycle.yaml')==identity['config_sha256']
histograms={}
for name,h in final['latency'].items():
 if not isinstance(h,dict) or 'buckets' not in h:continue
 assert sum(h['buckets'])==h['count']
 out={'count':h['count'],'mean_ms':h['sum_nanos']/h['count']/1e6 if h['count'] else None,'max_ms':h['max_nanos']/1e6}
 for q in [.99,.999]:
  key='p99' if q==.99 else 'p999'; rank=math.ceil(q*h['count']); total=0
  out[key+'_interval_ms']=None
  if rank:
   for i,n in enumerate(h['buckets']):
    total+=n
    if total>=rank:
     out[key+'_interval_ms']={'lower_exclusive':h['bucket_upper_nanos'][i-1]/1e6 if i else None,'upper_inclusive':max(h['bucket_upper_nanos'][i],h['max_nanos'] if i==len(h['buckets'])-1 else 0)/1e6};break
 if name=='hot_sendack':out['above_400ms']=sum(n for upper,n in zip(h['bucket_upper_nanos'],h['buckets']) if upper>400000000)
 histograms[name]=out
warnings=[]; panic_fatal=[]; warning_minutes=collections.Counter()
# Canonical per-node app.log prevents counting the duplicated service/warn sinks.
for path in sorted((root/'run/logs').glob('node*/app.log')):
 grouped={}
 for line_number,line in enumerate(path.read_text(errors='replace').splitlines(),1):
  if re.search(r'(?i)\b(panic|fatal)\b',line):panic_fatal.append({'path':str(path.relative_to(root)),'line':line_number})
  match=re.search(r'\t(WARN|ERROR|FATAL|PANIC)\t',line)
  if not match:continue
  parts=line.split('\t')
  try:body=json.loads(parts[-1])
  except json.JSONDecodeError:body={}
  selected={k:body[k] for k in ['event','result','error','component','phase'] if k in body}
  signature={'level':match.group(1),'logger':parts[2],'message':parts[-2],**selected}
  key=json.dumps(signature,sort_keys=True)
  x=grouped.setdefault(key,{'signature':signature,'count':0,'first_at':parts[0],'first_line':line_number,'last_at':parts[0],'last_line':line_number,'before_report_end':0,'after_report_end':0})
  x['count']+=1;x['last_at']=parts[0];x['last_line']=line_number
  moment=datetime.datetime.fromisoformat(parts[0]).replace(tzinfo=datetime.timezone.utc)
  x['before_report_end' if moment<=dt(final['window']['end']) else 'after_report_end']+=1
  warning_minutes[(path.parent.name,parts[0][:16],selected.get('event',''))]+=1
 warnings.append({'path':str(path.relative_to(root)),'groups':list(grouped.values())})
# Also scan all logs for panic/fatal while preserving only path/line booleans.
all_panic_fatal=[]
for path in sorted((root/'run/logs').rglob('*')):
 if path.is_file():
  for n,line in enumerate(path.read_text(errors='replace').splitlines(),1):
   if re.search(r'(?i)\b(panic|fatal)\b',line):all_panic_fatal.append({'path':str(path.relative_to(root)),'line':n})
start=dt(final['window']['start']); end=dt(final['window']['end'])
hosts={}; host_errors=collections.Counter(); process_errors=collections.Counter(); host_lines=process_lines=0
for line in (root/'observations/host-metrics.jsonl').read_text().splitlines():
 d=json.loads(line);host_lines+=1
 for t in d['targets']:
  if 'error' in t:host_errors[t.get('error_type','unknown')]+=1;continue
  if not start<=dt(t['started_at'])<=end:continue
  state=hosts.setdefault(str(t['port']),{'successful_measured_cuts':0,'metrics':{},'availability':{}});state['successful_measured_cuts']+=1
  for raw in t['content'].splitlines():
   if not raw.startswith('wkbench_'):continue
   fields=raw.rsplit(' ',1)
   try:value=float(fields[-1])
   except ValueError:continue
   name=fields[0]
   if name in ['wkbench_host_cpu_busy_percent','wkbench_host_memory_used_percent']:
    values=state['metrics'].setdefault(name,[]);values.append(value)
   if '_available' in name:state['availability'].setdefault(name,set()).add(value)
for state in hosts.values():
 for k,v in state['metrics'].items():state['metrics'][k]={'min':min(v),'max':max(v),'mean_of_observed_cuts':sum(v)/len(v),'last':v[-1]}
 state['availability']={k:sorted(v) for k,v in state['availability'].items()}
for line in (root/'observations/process-metrics.jsonl').read_text().splitlines():
 d=json.loads(line);process_lines+=1
 for v in d['files'].values():
  if 'error' in v:process_errors[v.get('error_type','unknown')]+=1
cuts=[]
for line in (root/'run/logs/coordinator.log').read_text().splitlines():
 try:d=json.loads(line)
 except json.JSONDecodeError:continue
 if d.get('event')=='wkbench.chat_lifecycle.worker_status_cut':cuts.append(d)
stat=os.statvfs(root)
summary={'status':'completed_local_operator_stop_with_logged_warnings','source_sha':command['source_sha'],'source_clean_before_and_after':result['source_unchanged'],'attempts':1,'helper_exit_code':result['helper_exit_code'],'wrapper_exit_code':result['wrapper_exit_code'],'helper_planned_operator_stop':result['helper_planned_operator_stop'],'formal_qualification':False,'helper_stop_after_seconds':command['stop_after_seconds'],'started_at':command['started_at'],'finished_at':command['finished_at'],'window':final['window'],'measured_duration_seconds':final['window']['elapsed']/1e9,'final_sha256':sha(root/'run/report/final.json'),'config_sha256':identity['config_sha256'],'binary_identity':binaries,'profile':final['profile'],'stage':final['stage'],'verdict':final['verdict'],'topology':final['topology'],'messages':final['messages'],'generated':final['generated'],'harness':final['harness'],'sessions':final['sessions'],'correlation':final['correlation'],'queues':final['queues'],'histogram_quantile_bounds':histograms,'cluster':final['cluster'],'resources':final['resources'],'lifecycle':final['lifecycle'],'warnings':final.get('warnings',[]),'native_log_warnings':warnings,'warning_minute_counts':[{'node':k[0],'minute_utc':k[1],'event':k[2],'count':v} for k,v in sorted(warning_minutes.items())],'panic_fatal_log_hits':all_panic_fatal,'observations':{'host_lines':host_lines,'process_lines':process_lines,'host_errors_including_startup_shutdown':dict(host_errors),'process_errors_including_startup_shutdown':dict(process_errors),'host_per_port_measured_cuts':hosts,'business_cuts':len(cuts),'business_first_cut':cuts[0]['at'],'business_last_cut':cuts[-1]['at'],'worker_cut_interval_seconds':5,'single_physical_host':True},'cleanup':{'pass':result['cleanup']['pass'],'recorded_processes':len(result['cleanup']['recorded_processes']),'all_recorded_pids_exited':not any(v['alive_same_identity'] for v in result['cleanup']['recorded_processes'].values()),'all_64_ports_closed':not any(v['open'] for v in result['cleanup']['ports'].values()),'owned_session':result['cleanup']['owned_session'],'post_available_percent':stat.f_bavail/stat.f_blocks*100,'task_tmp_empty':not any(Path(preflight['local_tmp']).iterdir())},'credential_scan':read('credential-scan.json'),'observer_errors':result['observer_errors'],'limitations':['Native macOS three-node cluster on one host, explicit rehearsal tuning; not Linux four-host formal qualification.','1800-second helper timer begins at coordinator process start; measured window is 1753.104 seconds. Native outcome is operator_stop, not pass.','Histogram intervals are bucket bounds, not exact P99/P999 samples. Histograms include all retained measured and initial samples; do not substitute them for per-window evaluator logic.','One high CPU sample on one logical host projection is retained; zero sustained CPU/memory/queue saturation events. Four exporters observe one physical Mac.','Forced-GC and goroutine retention do not cover 6-hour/24-hour growth windows; lifecycle cold/reheat proof and capacity staircase were not exercised.','94 node1 route-not-ready warnings include measured and shutdown phases; six component-stop timeout warnings occurred after final report. Zero benchmark errors and complete OS cleanup do not erase those warnings.','Credential scan only covers stated uncompressed <=64MiB evidence, not arbitrary filesystem contents.'],'load_retried':False,'thresholds_changed':False,'cloud_operations':False}
a.output.write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'output':str(a.output),'sha256':sha(a.output),'helper_exit':result['helper_exit_code'],'final_outcome':final['verdict']['outcome'],'cleanup':result['cleanup']['pass'],'log_warning_groups':sum(len(x['groups']) for x in warnings),'panic_fatal_count':len(all_panic_fatal)}))
