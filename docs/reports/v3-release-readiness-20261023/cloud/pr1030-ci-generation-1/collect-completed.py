#!/usr/bin/env python3
import pathlib,json,subprocess,concurrent.futures,hashlib,re,zipfile,io,stat,datetime
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1030-ci-generation-1'); P=sorted((R/'polls').iterdir())[-1]
(R/'logs').mkdir(exist_ok=True);(R/'verified-downloads').mkdir(exist_ok=True);(R/'PRIVATE_original-downloads').mkdir(exist_ok=True,mode=0o700)
sha=lambda x:hashlib.sha256(x).hexdigest()
def get(endpoint,limit=16<<20):
 import selectors,time,os
 proc=subprocess.Popen(['gh','api','--method','GET','repos/WuKongIM/WuKongIM/'+endpoint],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 sel=selectors.DefaultSelector();sel.register(proc.stdout,selectors.EVENT_READ,'out');sel.register(proc.stderr,selectors.EVENT_READ,'err')
 chunks={'out':[],'err':[]};sizes={'out':0,'err':0};started=time.monotonic()
 try:
  while sel.get_map():
   remaining=55-(time.monotonic()-started)
   if remaining<=0:raise TimeoutError(endpoint)
   for key,_ in sel.select(min(remaining,1)):
    buf=os.read(key.fd,65536)
    if not buf:sel.unregister(key.fileobj);continue
    kind=key.data;sizes[kind]+=len(buf)
    if sizes[kind]>(limit if kind=='out' else 65536):raise ValueError((endpoint,'output_limit',sizes))
    chunks[kind].append(buf)
  rc=proc.wait(timeout=max(1,55-(time.monotonic()-started)))
  if rc:raise RuntimeError({'endpoint':endpoint,'exit':rc,'stderr':b''.join(chunks['err']).decode(errors='replace')[:250]})
  assert sizes['out']>0
  return b''.join(chunks['out'])
 finally:
  sel.close()
  if proc.poll() is None:proc.kill();proc.wait()

def log(j):
 path=R/'logs'/('job-'+str(j['id'])+'.log')
 if path.exists():b=path.read_bytes()
 else:b=get('actions/jobs/'+str(j['id'])+'/logs');path.write_bytes(b)
 c=re.findall(rb'git log -1 --format=%H\r?\n[^\n]*?([0-9a-f]{40})',b)
 return {'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'bytes':len(b),'sha256':sha(b),'actual_checkout_candidates':[s.decode() for s in c]}
def artifact(a):
 name=a['name'];meta={'id':a['id'],'name':name,'declared_bytes':a['size_in_bytes'],'declared_digest':a.get('digest')}
 if not name.startswith(('v3-candidate-','500qps-')):return dict(meta,state='not_downloaded',reason='outside explicit proof allowlist; native package binaries and unrelated artifact contents not fetched')
 if a['size_in_bytes']>16<<20:return dict(meta,state='not_downloaded',reason='exceeds explicit 16 MiB small-evidence limit')
 # E2E archive may contain synthetic raw message and user identifiers. Keep original privately; expose only a finite typed summary later.
 private=name.startswith('v3-candidate-e2e-'); folder='PRIVATE_original-downloads' if private else 'verified-downloads'
 path=R/folder/(str(a['id'])+'.zip')
 if path.exists():b=path.read_bytes()
 else:b=get('actions/artifacts/'+str(a['id'])+'/zip');path.write_bytes(b);path.chmod(0o600 if private else 0o644)
 assert len(b)==a['size_in_bytes'];assert a['digest']=='sha256:'+sha(b)
 z=zipfile.ZipFile(io.BytesIO(b));info=z.infolist();assert len(info)<=256;assert sum(x.file_size for x in info)<=64<<20
 entries=[];assert len({x.filename for x in info})==len(info)
 for x in info:
  q=pathlib.PurePosixPath(x.filename);assert not q.is_absolute() and '..' not in q.parts and '\\' not in x.filename
  assert not stat.S_ISLNK(x.external_attr>>16) and not x.flag_bits&1 and x.file_size<=16<<20
  if x.is_dir():continue
  body=z.read(x);entries.append({'path':x.filename,'bytes':len(body),'compressed_bytes':x.compress_size,'crc32':format(x.CRC,'08x'),'sha256':sha(body)})
  if not private:
   assert x.filename.endswith(('.json','.txt','.log','.jsonl','.sha256','.sha')),('unexpected_nontext_member',name,x.filename)
   body.decode('utf-8');assert b'\x00' not in body,('unexpected_binary_member',name,x.filename)
   dst=R/'verified-artifacts'/str(a['id'])/x.filename;dst.parent.mkdir(parents=True,exist_ok=True);dst.write_bytes(body)
 assert z.testzip() is None
 receipt=dict(meta,state='verified_private_original' if private else 'verified',zip_bytes=len(b),zip_sha256=sha(b),full_size_sha_crc_valid=True,members=entries)
 (R/'verified-downloads'/(str(a['id'])+'-receipt.json')).write_text(json.dumps(receipt,indent=2)+'\n')
 return receipt
jobs=[j for f in P.glob('jobs-*.json') for j in json.loads(f.read_text())['jobs'] if j['status']=='completed' and j['steps']]
arts=[a for f in P.glob('artifacts-*.json') for a in json.loads(f.read_text())['artifacts']]
with concurrent.futures.ThreadPoolExecutor(max_workers=3) as ex:logs=list(ex.map(log,jobs))
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as ex:artifacts=list(ex.map(artifact,arts))
receipt={'snapshot':P.name,'logs':logs,'artifacts':artifacts};(R/('collected-'+P.name+'.json')).write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps({'snapshot':P.name,'logs':[(x['id'],x['name'],x['bytes']) for x in logs],'artifacts':[{'id':a['id'],'name':a['name'],'state':a['state'],'bytes':a['declared_bytes'],'member_count':len(a.get('members',[]))} for a in artifacts]}))
