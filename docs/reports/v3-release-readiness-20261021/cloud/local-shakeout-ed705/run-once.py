#!/usr/bin/env python3
"""Run the committed native local helper once; preserve scoped evidence."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import time

ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
OUT = Path(__file__).resolve().parent
RUN = OUT / 'run'
SOURCE = 'ed705c0fbffdea485bbb65608d6e340bb049d97e'
HELPER = ROOT / 'scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh'

def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()

def cmd(args, env=None):
    return subprocess.check_output(args, cwd=ROOT, env=env, text=True).strip()

def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda:f.read(1024*1024),b''):
            h.update(block)
    return h.hexdigest()

def write(name, data):
    (OUT / name).write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n')

assert not RUN.exists(), 'never reuse a run'
assert not (OUT / 'command.json').exists(), 'run-once receipt already exists'
assert cmd(['git','rev-parse','HEAD']) == SOURCE
assert cmd(['git','status','--porcelain']) == ''
files = ['AGENTS.md','test/e2e/AGENTS.md','internal/bench/FLOW.md','internal/bench/chatlifecycle/FLOW.md',
         'go.mod','go.sum','scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh',
         'scripts/chat-lifecycle/detect-local-workload-overlap.sh','scripts/chat-lifecycle/capture-local-storage-overlap.sh',
         'scripts/storage-metrics-cut-consistent.awk','configs/wkbench/chat-lifecycle/local-shakeout.yaml',
         'docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md']
files += [f'scripts/wukongim/wukongim-node{i}.toml' for i in [1,2,3]]
freeze = {name:sha(ROOT/name) for name in files}
stat = os.statvfs(OUT)
assert stat.f_bavail / stat.f_blocks >= .05
assert stat.f_bavail * stat.f_frsize > 20*1024**3, 'operator admission headroom only, not a changed workload gate'
overlap = cmd([str(ROOT/'scripts/chat-lifecycle/detect-local-workload-overlap.sh')])
assert not overlap, 'existing WuKongIM workload'
for port in range(15000,15064):
    with socket.socket() as s:
        s.bind(('127.0.0.1',port))
cache = OUT/'go-cache';cache.mkdir(mode=0o700)
short_tmp = Path('/tmp/wksoak-ed705-20261021');short_tmp.mkdir(mode=0o700)
env = {'PATH':'/usr/local/go/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin', 'HOME':str(Path.home()),
       'GOWORK':'off','GOENV':'off','GOTOOLCHAIN':'go1.25.11','GOFLAGS':'-p=2','GOCACHE':str(cache),
       'TMPDIR':str(short_tmp),'LC_ALL':'C','TZ':'UTC'}
version = cmd(['go','version'],env)
assert version == 'go version go1.25.11 darwin/arm64', version
arguments = [str(HELPER),'--run-dir',str(RUN),'--stop-after','1800']
dry = subprocess.run(arguments+['--dry-run'],cwd=ROOT,env=env,capture_output=True,text=True,check=True)
(OUT/'dry-run.stdout').write_text(dry.stdout)
(OUT/'dry-run.stderr').write_text(dry.stderr)
write('preflight.json',{'observed_at':now(),'source_sha':SOURCE,'source_clean':True,'source_files_sha256':freeze,
                      'worktree_reason':'No development or code edits; use exact clean merged primary checkout, freeze before/after builds and run.',
                      'go_version':version,'platform':cmd(['uname','-a']),
                      'memory_bytes':int(cmd(['sysctl','-n','hw.memsize'])),'logical_cpu_count':int(cmd(['sysctl','-n','hw.logicalcpu'])),
                      'filesystem':{'capacity_bytes':stat.f_blocks*stat.f_frsize,'available_bytes':stat.f_bavail*stat.f_frsize,'available_percent':stat.f_bavail/stat.f_blocks*100,'fixed_reserve_percent':5},
                      'localhost_ports_checked':[15000,15063],'existing_wukongim_workloads':[],
                      'config_change':False,'cloud_operation':False,'independent_gocache':str(cache),'local_tmp':str(short_tmp),
                      'network_scope':'Owned service, worker, manager, debug, metrics and gateway endpoints are bound to 127.0.0.1 by the committed helper.',
                      'evidence_level':'Local macOS native-process shakeout only; not Linux formal/rehearsal/72-hour qualification.'})
api_token = secrets.token_hex(32)
worker_token = secrets.token_hex(32)
env['WK_BENCH_API_TOKEN'] = api_token
env['WK_BENCH_WORKER_TOKEN'] = worker_token
receipt = {'command':arguments,'cwd':str(ROOT),'started_at':now(),'source_sha':SOURCE,
           'environment':{k:v for k,v in env.items() if not k.startswith('WK_')},
           'credential_keys':['WK_BENCH_API_TOKEN','WK_BENCH_WORKER_TOKEN'],
           'credentials':'Fresh distinct local-only 256-bit tokens; environment only, never persisted in this receipt.',
           'bound_seconds':3000,'stop_after_seconds':1800,'helper_pid':None,'exit_code':None}
with (OUT/'stdout-stderr.log').open('wb') as log:
    child = subprocess.Popen(arguments,cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
    receipt['helper_pid']=child.pid;write('command.json',receipt)
    print(json.dumps({'started':now(),'helper_pid':child.pid,'source_sha':SOURCE,'run_dir':str(RUN)}),flush=True)
    try:
        status = child.wait(timeout=3000)
    except subprocess.TimeoutExpired:
        receipt['outer_deadline_exceeded']=True
        child.send_signal(signal.SIGTERM)
        try:
            status=child.wait(timeout=120)
        except subprocess.TimeoutExpired:
            receipt['forced_owned_session_stop']=True
            os.killpg(child.pid,signal.SIGTERM)
            try:status=child.wait(timeout=20)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid,signal.SIGKILL);status=child.wait(timeout=20)
receipt.update({'exit_code':status,'finished_at':now(),'source_sha_after':cmd(['git','rev-parse','HEAD']),
                'source_clean_after':cmd(['git','status','--porcelain'])=='',
                'source_files_unchanged':freeze=={name:sha(ROOT/name) for name in files}})
write('command.json',receipt)
# Persist only paths and presence flags; never raw matching secret bytes.
leaks=[]
for base in [RUN/'logs',RUN/'config',RUN/'report',RUN/'evidence']:
    if base.exists():
        for f in base.rglob('*'):
            if f.is_file() and f.stat().st_size <= 64*1024*1024:
                b=f.read_bytes()
                if api_token.encode() in b or worker_token.encode() in b:leaks.append(str(f.relative_to(OUT)))
write('credential-scan.json',{'raw_token_match_paths':leaks,'scope':'Uncompressed regular logs/config/report/evidence files <=64MiB. No token values saved.','pass':not leaks})
print(json.dumps({'finished':now(),'exit_code':status,'raw_token_match_paths':leaks}),flush=True)
