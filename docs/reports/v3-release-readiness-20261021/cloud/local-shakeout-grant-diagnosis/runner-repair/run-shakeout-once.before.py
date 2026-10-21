#!/usr/bin/env python3
"""Run the committed native local helper once; preserve scoped evidence."""
import argparse
import datetime
import concurrent.futures
import threading
import urllib.request
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import time

parser = argparse.ArgumentParser()
parser.add_argument('--source-root', type=Path, required=True)
parser.add_argument('--output-root', type=Path, required=True)
parser.add_argument('--source-sha', required=True)
parser.add_argument('--cache-dir', type=Path, required=True)
args = parser.parse_args()
ROOT = args.source_root.resolve()
OUT = args.output_root.resolve()
OUT.mkdir(mode=0o700, exist_ok=False)
RUN = OUT / 'run'
SOURCE = args.source_sha
HELPER = ROOT / 'scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh'
(OUT/'run-once.py').write_bytes(Path(__file__).read_bytes())

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
cache = args.cache_dir.resolve();assert cache.is_dir()
short_tmp = Path('/tmp')/('wksoak-'+SOURCE[:12]+'-20261021');short_tmp.mkdir(mode=0o700)
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
                      'worktree_reason':'Use exact clean committed repair worktree; freeze before/after builds and run. Parent coordinates absence of competing task load.',
                      'go_version':version,'platform':cmd(['uname','-a']),
                      'memory_bytes':int(cmd(['sysctl','-n','hw.memsize'])),'logical_cpu_count':int(cmd(['sysctl','-n','hw.logicalcpu'])),
                      'filesystem':{'capacity_bytes':stat.f_blocks*stat.f_frsize,'available_bytes':stat.f_bavail*stat.f_frsize,'available_percent':stat.f_bavail/stat.f_blocks*100,'fixed_reserve_percent':5},
                      'localhost_ports_checked':[15000,15063],'existing_wukongim_workloads':[],
                      'config_change':'Committed local defaults 500 SEND/s and 1000 burst; no runtime override or gate changes','cloud_operation':False,'independent_gocache':str(cache),'local_tmp':str(short_tmp),
                      'network_scope':'Owned service, worker, manager, debug, metrics and gateway endpoints are bound to 127.0.0.1 by the committed helper.',
                      'evidence_level':'Local macOS native-process shakeout only; not Linux formal/rehearsal/72-hour qualification.'})
# These observational receipts do not alter workload, product metrics or verdicts.
# Existing coordinator business cuts are already retained every five seconds.
observer_stop = threading.Event()
observations = OUT/'observations';observations.mkdir()
owned = {}
def append(name, data):
    with (observations/name).open('a') as f:f.write(json.dumps(data)+'\n')
def collect_host(port):
    d={'started_at':now(),'port':port}
    try:
        with urllib.request.urlopen(f'http://127.0.0.1:{port}/metrics',timeout=2) as response:
            payload=response.read(65537)
        if len(payload)>65536:raise ValueError('existing host metrics exceed 64KiB capture bound')
        d['content']=payload.decode('utf-8');d['sha256']=hashlib.sha256(payload).hexdigest()
    except Exception as e:d['error_type']=type(e).__name__;d['error']=str(e)
    d['finished_at']=now();return d
def observe():
    start=time.monotonic();next_process=start;next_host=start
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        while not observer_stop.is_set():
            stamp=time.monotonic()
            if stamp>=next_process:
                cut={'observed_at':now(),'offset_seconds':stamp-start,'files':{}}
                for name in ['processes-node-1.prom','processes-node-2.prom','processes-node-3.prom','processes-load.prom']:
                    path=RUN/'evidence'/name
                    try:
                        data=path.read_bytes()
                        if len(data)>65536:raise ValueError('process metrics exceed 64KiB capture bound')
                        cut['files'][name]={'mtime_ns':path.stat().st_mtime_ns,'content':data.decode(),'sha256':hashlib.sha256(data).hexdigest()}
                    except Exception as e:cut['files'][name]={'error_type':type(e).__name__,'error':str(e)}
                append('process-metrics.jsonl',cut)
                for path in sorted((RUN/'pids').glob('*.pid')):
                    try:
                        pid=int(path.read_text().strip())
                        if str(pid) not in owned:
                            identity=subprocess.check_output(['ps','-p',str(pid),'-o','pid=,lstart=,comm='],text=True).strip()
                            owned[str(pid)]={'name':path.stem,'pid':pid,'first_seen':now(),'ps_identity':identity}
                            write('owned-process-identities.json',owned)
                    except (ValueError,OSError,subprocess.CalledProcessError):pass
                next_process=stamp+5
            if stamp>=next_host:
                append('host-metrics.jsonl',{'observed_at':now(),'offset_seconds':stamp-start,'targets':list(pool.map(collect_host,range(15060,15064)))})
                next_host=stamp+15
            observer_stop.wait(.25)
observer_errors=[]
def observed_capture():
    try:observe()
    except Exception as e:observer_errors.append({'error_type':type(e).__name__,'error':str(e),'observed_at':now()})
observer = threading.Thread(target=observed_capture,daemon=True)
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
    observer.start()
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
observer_stop.set();observer.join(timeout=15)
assert not observer.is_alive(), 'bounded evidence observer failed to stop'
write('observation-summary.json',{'process_capture_interval_seconds':5,'host_capture_interval_seconds':15,'host_ports':[15060,15061,15062,15063],'per_response_bytes_max':65536,'endpoint_timeout_seconds':2,'errors':observer_errors,'source':'Existing loopback host-metrics and process.prom files only; no product metrics scrape or profile capture.'})
receipt.update({'exit_code':status,'finished_at':now(),'source_sha_after':cmd(['git','rev-parse','HEAD']),
                'source_clean_after':cmd(['git','status','--porcelain'])=='',
                'source_files_unchanged':freeze=={name:sha(ROOT/name) for name in files}})
write('command.json',receipt)
cleanup={'observed_at':now(),'recorded_processes':{},'ports':{},'observer_stopped':True}
for key,identity in owned.items():
    result=subprocess.run(['ps','-p',key,'-o','pid=,lstart=,comm='],capture_output=True,text=True)
    cleanup['recorded_processes'][key]={'name':identity['name'],'alive_same_identity':result.returncode==0 and result.stdout.strip()==identity['ps_identity'],'current_identity':result.stdout.strip()}
for port in range(15000,15064):
    with socket.socket() as probe:
        probe.settimeout(.2);cleanup['ports'][str(port)]={'open':probe.connect_ex(('127.0.0.1',port))==0}
cleanup['pass']=not any(p['alive_same_identity'] for p in cleanup['recorded_processes'].values()) and not any(p['open'] for p in cleanup['ports'].values())
write('cleanup-verification.json',cleanup)
# Persist only paths and presence flags; never raw matching secret bytes.
leaks=[]
for base in [RUN/'logs',RUN/'config',RUN/'report',RUN/'evidence',observations]:
    if base.exists():
        for f in base.rglob('*'):
            if f.is_file() and f.stat().st_size <= 64*1024*1024:
                b=f.read_bytes()
                if api_token.encode() in b or worker_token.encode() in b:leaks.append(str(f.relative_to(OUT)))
for f in OUT.iterdir():
    if f.is_file() and f.stat().st_size <= 64*1024*1024:
        b=f.read_bytes()
        if api_token.encode() in b or worker_token.encode() in b:leaks.append(str(f.relative_to(OUT)))
write('credential-scan.json',{'raw_token_match_paths':leaks,'scope':'Uncompressed regular logs/config/report/evidence/observations files and top-level evidence files <=64MiB. No token values saved.','pass':not leaks})
print(json.dumps({'finished':now(),'exit_code':status,'raw_token_match_paths':leaks}),flush=True)
