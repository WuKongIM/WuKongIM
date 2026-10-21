#!/usr/bin/env python3
"""Read frozen shakeout receipts only; write a new bounded, identity-free excerpt."""
import argparse, datetime as dt, hashlib, json, pathlib, re, subprocess
p=argparse.ArgumentParser();p.add_argument('--evidence-root',type=pathlib.Path,required=True);p.add_argument('--source-root',type=pathlib.Path,required=True);p.add_argument('--output',type=pathlib.Path,required=True);a=p.parse_args()
def h(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def t(s):return dt.datetime.fromisoformat(s.replace('Z','+00:00'))
def j(p):return json.loads(p.read_text())
raw=a.evidence_root/'run/logs/coordinator.log'
cuts=[]
for n,line in enumerate(raw.read_text().splitlines(),1):
 try:d=json.loads(line)
 except ValueError:continue
 if d.get('event')=='wkbench.chat_lifecycle.worker_status_cut':d['source_line']=n;cuts.append(d)
last=cuts[-1]; recent=[x for x in cuts if (t(last['at'])-t(x['at'])).total_seconds()<=120.01]
first=recent[0];messages=['sent','send_attempts','first_attempts','first_attempt_failures','send_acknowledged','send_rejected','received','receive_acknowledged','receive_ack_failures','retry_attempts','terminal','losses','duplicates','corruptions','sequence_regressions']
rows=[]
for x in recent:
 rows.append({k:x[k] for k in ['at','source_line','totals','close_reasons','hot_latency','harness']}|{'messages':{k:x['messages'][k] for k in messages}})
diagnostic=j(a.evidence_root/'run/report/diagnostic-status.json')
processes={}
for file in sorted((a.evidence_root/'run/evidence').glob('processes-*.prom')):
 data={}
 for line in file.read_text().splitlines():
  m=re.fullmatch(r'(wukongim_process_\w+)(?:\{unit="([^"]+)"\})? ([\d.]+)',line)
  if m:data.setdefault(m[2] or 'collector',{})[m[1]]=float(m[3])
 processes[file.name]={'sha256':h(file),'metrics':data}
scan=j(a.evidence_root/'credential-scan.json')
summary=j(a.evidence_root/'summary.json')
files=['AGENTS.md','CONTEXT.md','internal/bench/FLOW.md','internal/bench/chatlifecycle/FLOW.md','internal/bench/chatlifecycle/engine.go','internal/bench/chatlifecycle/worker_server.go','internal/bench/chatlifecycle/coordinator.go','internal/bench/chatlifecycle/grant_pacing_test.go','internal/bench/chatlifecycle/engine_test.go','configs/wkbench/chat-lifecycle/local-shakeout.yaml','docs/adr/0024-protect-results-from-load-generator-saturation.md','docs/adr/0046-report-resource-saturation-as-a-capacity-warning.md','docs/adr/0041-use-four-postpaid-hosts-for-automated-chat-lifecycle.md']
result={'schema':'wukongim/local-shakeout-readonly-diagnosis/v1','source_sha':subprocess.check_output(['git','rev-parse','HEAD'],cwd=a.source_root,text=True).strip(),'source_status':subprocess.check_output(['git','status','--porcelain'],cwd=a.source_root,text=True),'source_sha256':{f:h(a.source_root/f) for f in files},'frozen_evidence_summary_sha256':h(a.evidence_root/'summary.json'),'coordinator_log_sha256':h(raw),'first_worker_failure':summary['first_worker_failure'],'terminal_line':summary['terminal_line'],'window':{'from':first['at'],'to':last['at'],'duration_seconds':(t(last['at'])-t(first['at'])).total_seconds(),'messages_delta':{k:last['messages'][k]-first['messages'][k] for k in messages},'hot_count_delta':last['hot_latency']['count']-first['hot_latency']['count'],'hot_above_limit_delta':last['hot_latency']['above_limit']-first['hot_latency']['above_limit'],'cuts':rows},'diagnostic_updated_at':diagnostic['updated_at'],'last_workers':diagnostic['workers'],'recent_connection_events':diagnostic['recent_events'],'last_process_snapshots':processes,'available_metrics_files':sorted(str(p.relative_to(a.evidence_root)) for p in (a.evidence_root/'run/metrics').rglob('*') if p.is_file()),'pprof_files':sorted(str(p.relative_to(a.evidence_root)) for p in (a.evidence_root/'run').rglob('*') if p.is_file() and (p.suffix in ['.pprof','.prof'] or 'pprof' in p.name)),'secret_scan':{'pass':scan['pass'],'raw_token_match_found':bool(scan['raw_token_match_paths']),'scope':scan['scope']},'limitations':['No per-grant success trace or exact runtime failure branch was persisted.','Latest status cut predates terminal failure; no final report or final drain proof.','Only initial warmup-before host snapshots exist. Last process snapshots are cumulative CPU counters and RSS gauges, not failure-window CPU pressure/host memory/IO series.','No pprof file exists. Session expiry is a temporal association, not root-cause evidence.','No load rerun, test execution, product mutation or thresholds change in this read-only analysis.']}
with a.output.open('x') as f:json.dump(result,f,ensure_ascii=False,indent=2);f.write('\n')
print(json.dumps({'output':str(a.output),'sha256':h(a.output),'cut_count':len(rows),'window':{k:v for k,v in result['window'].items() if k!='cuts'},'pprof_count':len(result['pprof_files'])},ensure_ascii=False))
