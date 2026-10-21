#!/usr/bin/env python3
"""Read one bounded, credential-free progress cut from owned local evidence."""
from pathlib import Path
import datetime
import json
import os

root = Path(__file__).resolve().parent
run = root / 'run'
status = {'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
diag = run / 'report/diagnostic-status.json'
if diag.exists():
    d = json.loads(diag.read_text())
    status.update({k:d.get(k) for k in ['state','stage','started_at','updated_at','totals','close_reasons','harness']})
log = run / 'logs/coordinator.log'
if log.exists():
    with log.open('rb') as f:
        f.seek(max(0,log.stat().st_size-32768));tail=f.read().decode(errors='replace')
    for line in reversed(tail.splitlines()):
        try:d=json.loads(line)
        except json.JSONDecodeError:continue
        if d.get('event') == 'wkbench.chat_lifecycle.worker_status_cut':
            status['messages']=d.get('messages');status['hot_latency']=d.get('hot_latency');break
final=run/'report/final.json'
if final.exists():
    d=json.loads(final.read_text());status['final']={k:d.get(k) for k in ['profile','stage','kind','final','window','verdict','topology']}
s=os.statvfs(run);status['free_percent']=s.f_bavail/s.f_blocks*100
command=json.loads((root/'command.json').read_text());status['helper_exit_code']=command['exit_code']
with (root/'progress.jsonl').open('a') as f:f.write(json.dumps(status)+'\n')
print(json.dumps(status))
