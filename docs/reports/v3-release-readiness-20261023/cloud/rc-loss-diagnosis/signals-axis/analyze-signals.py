import json,hashlib,collections,datetime,re,subprocess
from pathlib import Path
P=Path('/tmp/wukongim-v3-readiness-20261021/cloud/pr1024-authorized-run-resume-20261023/status-check-20261023/public-v1')
O=Path('/tmp/wukongim-v3-readiness-20261023/cloud/rc-loss-diagnosis/signals-axis')
S=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis')
report=json.loads((P/'terminal/final.json').read_text()); start=datetime.datetime.fromisoformat(report['window']['start']).timestamp();end=datetime.datetime.fromisoformat(report['window']['end']).timestamp()
def metric_rows(name): return json.loads((P/f'terminal/evidence/metrics/range-{name}.json').read_text())['data']['result']
def summarize(row):
 v=[(float(t),float(x)) for t,x in row['values'] if start<=float(t)<=end]
 if not v:return None
 return {'labels':row['metric'],'points':len(v),'first_epoch':v[0][0],'last_epoch':v[-1][0],'first':v[0][1],'last':v[-1][1],'minimum':min(x for t,x in v),'maximum':max(x for t,x in v),'delta':v[-1][1]-v[0][1],'regressions':sum(v[i][1]<v[i-1][1] for i in range(1,len(v))),'max_adjacent_delta_per_second':max([(v[i][1]-v[i-1][1])/(v[i][0]-v[i-1][0]) for i in range(1,len(v))] or [0])}
res={'window':{'start':start,'end':end,'start_exact':report['window']['start'],'end_exact':report['window']['end'],'note':'Raw query_range evaluation points only. No interpolation, rate() extrapolation, or claim of exact startup/end cut.'},'metric_inventory':{},'up':[],'process':[],'queue_pressure':[],'non_success_label_counters':[],'delivery_process_results':[],'log_summary':[]}
for family in ['presence','delivery','up','runtime','channel_worker','process','counters','go']:
 rows=metric_rows(family); res['metric_inventory'][family]={'series':len(rows),'names':dict(collections.Counter(r['metric'].get('__name__','') for r in rows))}
 for row in rows:
  sm=summarize(row)
  if not sm: continue
  n=row['metric'].get('__name__','')
  if family=='up':res['up'].append(sm)
  if family=='process':res['process'].append(sm)
  if ('queue_depth' in n or 'queue_capacity' in n or 'inflight' in n or n.endswith('_workers') or n.endswith('_capacity')):res['queue_pressure'].append(sm)
  if n.endswith('_total') and row['metric'].get('result','ok') not in ['ok','accepted','success'] and sm['maximum']>0:res['non_success_label_counters'].append(sm)
  if n=='wukongim_delivery_recipient_worker_process_total':res['delivery_process_results'].append(sm)
for f in ['load.log','service-1.log','service-2.log','service-3.log']:
 lines=(P/'terminal/evidence/logs'/f).read_text().splitlines(); warnings=[];errors=[];events=collections.Counter()
 for idx,l in enumerate(lines,1):
  if ' WARN ' in l or 'level=WARN' in l:warnings.append((idx,l))
  if ' ERROR ' in l or 'level=ERROR' in l or 'panic:' in l or 'Out of memory' in l:errors.append((idx,l))
  m=re.search(r'"event":\s*"([^"]+)"',l)
  if m:events[m[1]]+=1
 res['log_summary'].append({'file':f,'lines':len(lines),'first':lines[0][:25] if lines else None,'last':lines[-1][:25] if lines else None,'warning_count':len(warnings),'error_panic_oom_count':len(errors),'events':dict(events),'warning_lines':[x[0] for x in warnings],'first_warning_at':warnings[0][1][:25] if warnings else None,'last_warning_at':warnings[-1][1][:25] if warnings else None})
(O/'limited-summary.json').write_text(json.dumps(res,indent=2)+'\n')
sourcefiles=['AGENTS.md','internal/bench/FLOW.md','internal/bench/chatlifecycle/FLOW.md','internal/usecase/clouddeploy/FLOW.md','pkg/metrics/registry.go','pkg/metrics/node_resource.go','pkg/metrics/channel.go','pkg/metrics/channel_runtime.go','scripts/chat-lifecycle/terminal-evidence.py','scripts/chat-lifecycle/collect-terminal-evidence.sh','internal/usecase/clouddeploy/templates.go','internal/bench/chatlifecycle/disk_observer.go']
binding={'source_head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=S,text=True).strip(),'source_tree':subprocess.check_output(['git','rev-parse','HEAD^{tree}'],cwd=S,text=True).strip(),'source_status':subprocess.check_output(['git','status','--porcelain'],cwd=S,text=True),'rules_read':[p for p in sourcefiles if p.endswith(('AGENTS.md','FLOW.md'))],'source_sha256':{p:hashlib.sha256((S/p).read_bytes()).hexdigest() for p in sourcefiles},'public_evidence_root':str(P),'public_manifest_sha256':hashlib.sha256((P/'SHA256SUMS').read_bytes()).hexdigest(),'public_manifest_verified_entries':49,'private_archives_read':False,'live_operations_performed':False,'source_mutation_performed':False}
(O/'source-binding.json').write_text(json.dumps(binding,indent=2)+'\n')
