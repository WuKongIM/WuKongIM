#!/usr/bin/env python3
"""Offline reproduction: decode existing profiles and independently normalize all five blocks."""
import argparse, collections, datetime, hashlib, json, os, pathlib, shutil, subprocess, statistics

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--artifact-root',type=pathlib.Path,required=True)
parser.add_argument('--go',default='go',help='Installed Go >=1.25 executable; automatic downloads are disabled')
parser.add_argument('--repo',type=pathlib.Path,default=pathlib.Path.cwd())
parser.add_argument('--output-dir',type=pathlib.Path,required=True,help='A new directory; existing evidence is never overwritten')
args=parser.parse_args()
source=pathlib.Path(__file__).resolve().parent
out=args.output_dir.resolve()
if out.exists(): parser.error('output-dir already exists; choose a new directory')
out.mkdir(parents=True)
root=args.artifact_root.resolve()
blocks=['03-A','04-B','05-B','06-A','07-C']
go=shutil.which(args.go)
if go is None: parser.error('Go executable not found')
offline_env=os.environ | {'GOWORK':'off','GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off'}
(out/'decoder-toolchain.txt').write_text(subprocess.check_output([go,'version'],env=offline_env,text=True))
files=[root/'blocks'/b/f for b in blocks for f in ['driver-cpu.pprof',*[f'node{n}-{kind}.pprof' for n in range(1,4) for kind in ['cpu','alloc-before','alloc-after']]]]
raw=json.loads(subprocess.check_output([go,'run',str(source/'extract.go'),*map(str,files)],cwd=args.repo,env=offline_env,text=True))
(out/'profile-data.json').write_text(json.dumps(raw,indent=2)+'\n')
profiles={p['file']:p for p in raw}
command_log=[]

def digest(path):
 with path.open('rb') as h: return hashlib.file_digest(h,'sha256').hexdigest()

def sum_maps(maps):
 r=collections.defaultdict(float)
 for mapping in maps:
  for k,v in mapping.items():r[k]+=v
 return dict(r)

def cpu(p):
 seconds=p['duration_ns']/1e9
 return {'sampled_cpu_seconds':p['total']/1e9,'duration_seconds':seconds,'sampled_cpu_cores':p['total']/p['duration_ns'],
         'start_utc':datetime.datetime.fromtimestamp(p['time_ns']/1e9,datetime.timezone.utc).isoformat(),
         'end_utc':datetime.datetime.fromtimestamp((p['time_ns']+p['duration_ns'])/1e9,datetime.timezone.utc).isoformat(),
         'flat_core_rates':{k:v/p['duration_ns'] for k,v in p['flat'].items()},
         'cumulative_core_rates':{k:v/p['duration_ns'] for k,v in p['cumulative'].items()},
         'labels_core_rates':{k:{name:v/p['duration_ns'] for name,v in vals.items()} for k,vals in p['labels'].items()}}

def top(mapping,count=25):return [{'function':k,'value':v} for k,v in sorted(mapping.items(),key=lambda pair:pair[1],reverse=True)[:count]]

summary={}
for b in blocks:
 report=json.loads((root/'blocks'/b/'report.json').read_text())
 role=b[-1];binary=root/'binaries'/role/'wukongim-e2e'
 assert digest(binary)==report['binary_sha256']
 driver=root/'harness/conversation_qps.test'
 assert digest(driver)==report['fixed_environment']['driver']['binary_sha256']
 driver_p=profiles[str(root/'blocks'/b/'driver-cpu.pprof')]
 driver_info=cpu(driver_p)
 nodes=[]
 for n in range(1,4):
  stem=root/'blocks'/b/f'node{n}'
  node=cpu(profiles[str(stem)+'-cpu.pprof'])
  before=profiles[str(stem)+'-alloc-before.pprof'];after=profiles[str(stem)+'-alloc-after.pprof']
  node['alloc_snapshot_seconds']=(after['time_ns']-before['time_ns'])/1e9
  node['sampled_alloc_delta_bytes']=after['total']-before['total']
  for metric in ['flat','cumulative']:
   node['alloc_delta_'+metric]={k:after[metric].get(k,0)-before[metric].get(k,0) for k in set(after[metric])|set(before[metric])}
  nodes.append(node)
 server_rates={metric:sum_maps([n[metric+'_core_rates'] for n in nodes]) for metric in ['flat','cumulative']}
 server_alloc={metric:sum_maps([n['alloc_delta_'+metric] for n in nodes]) for metric in ['flat','cumulative']}
 w=report['profile_window'];assert report['complete'] and all(p['errors']==0 and p['dropped']==0 for p in w['phases']);completed=sum(p['completed'] for p in w['phases'])
 summary[b]={
  'product_sha':report['product_sha'],'binary_sha256':report['binary_sha256'],'harness_sha':report['harness_sha'],
  'original_window_end':report['windows'][-1]['end'],
  'original_window_median_p99_ms':[statistics.median(w['phases'][j]['p99_ms'] for w in report['windows']) for j in range(2)],
  'profile_window':{k:w[k] for k in ['start','end','cpu_seconds','allocated_bytes','heap_bytes']},
  'profile_completed':completed,'window_cpu_ms_per_completed_request':w['cpu_seconds']*1000/completed,
  'window_alloc_bytes_per_completed_request':w['allocated_bytes']/completed,
  'window_phase_results':[{k:p.get(k) for k in ['case','completed','actual_qps','p99_ms','request_p99_ms','driver_wait_p99_ms','errors','dropped','verdict']} for p in w['phases']],
  'driver':driver_info,'nodes':nodes,
  'server_sampled_cpu_cores':sum(n['sampled_cpu_cores'] for n in nodes),
  'server_sampled_alloc_delta_bytes':sum(n['sampled_alloc_delta_bytes'] for n in nodes),
  'server_cpu_core_rates':server_rates,'server_alloc_delta_bytes':server_alloc,
  'server_top_cpu_cumulative':top(server_rates['cumulative']),
  'server_top_cpu_flat':top(server_rates['flat']),
  'driver_top_cpu_cumulative':top(driver_info['cumulative_core_rates']),
  'server_top_alloc_cumulative':top(server_alloc['cumulative']),
  'server_top_alloc_flat':top(server_alloc['flat']),
 }
 # Retain primary pprof presentations against the actual Linux binaries. No workload is executed.
 for name,binary_path,ps in [('server',binary,[root/'blocks'/b/f'node{n}-cpu.pprof' for n in range(1,4)]),('driver',driver,[root/'blocks'/b/'driver-cpu.pprof'])]:
  for view in ['cum','flat']:
   command=[go,'tool','pprof','-top','-nodecount=40','-nodefraction=0','-unit=seconds']+(['-cum'] if view=='cum' else [])+[str(binary_path),*map(str,ps)]
   result=subprocess.run(command,cwd=args.repo,env=offline_env,capture_output=True,text=True,check=True)
   (out/f'{b}-{name}-{view}.txt').write_text(result.stdout+result.stderr)
   command_log.append(command)
 for n in range(1,4):
  command=[go,'tool','pprof','-top','-cum','-nodecount=30','-nodefraction=0','-unit=bytes','-sample_index=alloc_space','-base',str(root/'blocks'/b/f'node{n}-alloc-before.pprof'),str(binary),str(root/'blocks'/b/f'node{n}-alloc-after.pprof')]
  result=subprocess.run(command,cwd=args.repo,env=offline_env,capture_output=True,text=True,check=True)
  (out/f'{b}-node{n}-alloc-cum.txt').write_text(result.stdout+result.stderr)
  command_log.append(command)

(out/'profile-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
(out/'commands.json').write_text(json.dumps(command_log,indent=2)+'\n')
(out/'input-sha256.json').write_text(json.dumps({str(p):digest(p) for p in files},indent=2)+'\n')
for b,s in summary.items():
 print(b,'driver',round(s['driver']['sampled_cpu_cores'],4),'server',round(s['server_sampled_cpu_cores'],4),
       'CPUms/req',round(s['window_cpu_ms_per_completed_request'],4),'alloc/req',round(s['window_alloc_bytes_per_completed_request'],2),
       'profile snapshot MB',round(s['server_sampled_alloc_delta_bytes']/1e6,2))
