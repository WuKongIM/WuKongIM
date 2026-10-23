from pathlib import Path,PurePosixPath
import json,datetime,hashlib,subprocess,selectors,os,time,re,zipfile,io,stat
B=Path(__file__).parent;P=B.with_name(B.name+'-private');O=sorted((B/'polls').iterdir())[-1]
(P/'logs').mkdir(exist_ok=True,mode=0o700);(P/'archives').mkdir(exist_ok=True,mode=0o700);(P/'download-errors').mkdir(exist_ok=True,mode=0o700)
(B/'proof-inventory').mkdir(exist_ok=True);(B/'log-bindings').mkdir(exist_ok=True)
sha=lambda b:hashlib.sha256(b).hexdigest()
def write(p,v):
 with p.open('x') as f:json.dump(v,f,indent=2);f.write('\n')
def get(endpoint,key,limit):
 proc=subprocess.Popen(['gh','api','--method','GET','repos/WuKongIM/WuKongIM/'+endpoint],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 sel=selectors.DefaultSelector();sel.register(proc.stdout,selectors.EVENT_READ,'stdout');sel.register(proc.stderr,selectors.EVENT_READ,'stderr');chunks={'stdout':[],'stderr':[]};sizes={'stdout':0,'stderr':0};start=time.monotonic();problem=None;rc=None
 try:
  while sel.get_map():
   remain=55-(time.monotonic()-start)
   if remain<=0:raise TimeoutError('bounded GET timeout')
   for sk,_ in sel.select(min(remain,1)):
    b=os.read(sk.fd,65536)
    if not b:sel.unregister(sk.fileobj);continue
    k=sk.data;sizes[k]+=len(b)
    if sizes[k]>(limit if k=='stdout' else 65536):raise ValueError('bounded GET output limit')
    chunks[k].append(b)
  rc=proc.wait(timeout=max(1,55-(time.monotonic()-start)))
  if rc:problem='nonzero_GET_exit'
 except Exception as e:problem=type(e).__name__
 finally:
  sel.close()
  if proc.poll() is None:proc.kill();proc.wait()
 data=b''.join(chunks['stdout']);err=b''.join(chunks['stderr'])
 q=P/'download-errors'/(key+'-'+datetime.datetime.now(datetime.timezone.utc).strftime('%H%M%S%f'))
 for suffix,b in [('stdout',data),('stderr',err)]:
  with Path(str(q)+'.'+suffix).open('xb') as f:os.chmod(f.name,0o600);f.write(b)
 rec={'endpoint':endpoint,'method':'GET','exit_code':rc,'problem':problem,'stdout_bytes':len(data),'stdout_sha256':sha(data),'stderr_bytes':len(err),'stderr_sha256':sha(err),'private_raw_only':True,'all_owned_processes_terminal':True}
 write(B/'proof-inventory'/(q.name+'.read.json'),rec)
 if problem:return None
 return data
jobs=[j for f in O.glob('jobs-*.json') if not f.name.endswith('.read.json') for j in json.loads(f.read_text())['jobs'] if j['status']=='completed' and j['steps']]
logs=[]
for j in jobs:
 target=B/'log-bindings'/('job-'+str(j['id'])+'.json');raw=P/'logs'/('job-'+str(j['id'])+'.log')
 if target.exists():continue
 b=raw.read_bytes() if raw.exists() else get('actions/jobs/'+str(j['id'])+'/logs','job-'+str(j['id']),16<<20)
 if b is None:continue
 if not raw.exists():raw.write_bytes(b);raw.chmod(0o600)
 body=b.decode('utf-8');checkout=[x.decode() for x in re.findall(rb'git log -1 --format=%H\r?\n[^\n]*?([0-9a-f]{40})',b)]
 v={'id':j['id'],'run_id':j['run_id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'bytes':len(b),'sha256':sha(b),'actual_checkout_candidates':checkout,'raw_logs':'private sibling excluded','successful_steps':[x['name'] for x in j['steps'] if x['conclusion']=='success'],'skipped_steps':[x['name'] for x in j['steps'] if x['conclusion']=='skipped']};write(target,v);logs.append(v)
arts=[a for f in O.glob('artifacts-*.json') if not f.name.endswith('.read.json') for a in json.loads(f.read_text())['artifacts']]
spent=sum(p.stat().st_size for p in (P/'archives').glob('*.zip'));downloaded=[]
for a in arts:
 aid=str(a['id']);target=B/'proof-inventory'/(aid+'.json')
 if target.exists():continue
 meta={k:a.get(k) for k in ['id','name','size_in_bytes','digest','expired']}
 if not a['name'].startswith(('v3-candidate-','500qps-')):
  write(target,dict(meta,state='metadata_only',reason='large correctness/native binary package or outside small proof allowlist',full_archive_hash_locally_recomputed=False));continue
 if spent+a['size_in_bytes']>16<<20:
  write(target,dict(meta,state='metadata_only',reason='combined 16MiB small ZIP budget',full_archive_hash_locally_recomputed=False));continue
 raw=P/'archives'/(aid+'.zip');b=raw.read_bytes() if raw.exists() else get('actions/artifacts/'+aid+'/zip','artifact-'+aid,min(a['size_in_bytes']+4096,16<<20))
 if b is None:continue
 assert len(b)==a['size_in_bytes'] and a['digest']=='sha256:'+sha(b)
 if not raw.exists():raw.write_bytes(b);raw.chmod(0o600);spent+=len(b)
 z=zipfile.ZipFile(io.BytesIO(b));members=z.infolist();assert len(members)<=256 and sum(x.file_size for x in members)<=64<<20 and len({x.filename for x in members})==len(members)
 inventory=[]
 for x in members:
  q=PurePosixPath(x.filename);assert not q.is_absolute() and '..' not in q.parts and '\\' not in x.filename and not stat.S_ISLNK(x.external_attr>>16) and not x.flag_bits&1 and x.file_size<=16<<20
  if x.is_dir():continue
  data=z.read(x);text_member=x.filename.endswith(('.json','.txt','.log','.jsonl','.sha256','.sha'))
  if text_member:data.decode('utf-8');assert b'\0' not in data
  inventory.append({'path':x.filename,'bytes':len(data),'compressed_bytes':x.compress_size,'crc32':format(x.CRC,'08x'),'sha256':sha(data),'utf8_text':text_member})
 assert z.testzip() is None
 v=dict(meta,state='verified_private_original',zip_sha256=sha(b),full_size_sha_crc_valid=True,members=inventory,member_count=len(inventory),raw_identity_profiles_logs_excluded_from_public=True);write(target,v);downloaded.append({'id':aid,'name':a['name'],'zip_bytes':len(b),'members':len(inventory)})
print(json.dumps({'snapshot':O.name,'new_log_bindings':[{'id':j['id'],'name':j['name'],'checkout':j['actual_checkout_candidates']} for j in logs],'new_small_proofs':downloaded,'total_small_ZIP_bytes':spent,'limit_bytes':16<<20}))
