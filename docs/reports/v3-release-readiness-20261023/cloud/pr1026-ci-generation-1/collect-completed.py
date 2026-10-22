#!/usr/bin/env python3
import pathlib,json,subprocess,concurrent.futures,hashlib,re,zipfile,io,stat
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1026-ci-generation-1');p=sorted((R/'polls').iterdir())[-1];(R/'logs').mkdir(exist_ok=True);(R/'verified-downloads').mkdir(exist_ok=True)
sha=lambda x:hashlib.sha256(x).hexdigest()
def get(endpoint):
 x=subprocess.run(['gh','api','--method','GET','repos/WuKongIM/WuKongIM/'+endpoint],capture_output=True,timeout=60)
 if x.returncode:raise RuntimeError({'endpoint':endpoint,'exit':x.returncode,'stderr':x.stderr.decode()[:250]})
 assert 0<len(x.stdout)<=16<<20
 return x.stdout
def log(j):
 path=R/'logs'/('job-'+str(j['id'])+'.log')
 if path.exists():b=path.read_bytes()
 else:b=get('actions/jobs/'+str(j['id'])+'/logs');path.write_bytes(b)
 c=re.findall(rb'git log -1 --format=%H\r?\n[^\n]*?([0-9a-f]{40})',b)
 return {'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'bytes':len(b),'sha256':sha(b),'actual_checkout_candidates':[s.decode() for s in c]}
def artifact(a):
 if a['size_in_bytes']>16<<20:return {'id':a['id'],'name':a['name'],'state':'not_downloaded','reason':'exceeds explicit 16 MiB small-evidence limit','declared_bytes':a['size_in_bytes'],'declared_digest':a.get('digest')}
 name=a['name'];assert name.startswith(('v3-candidate-','500qps-'))
 path=R/'verified-downloads'/(str(a['id'])+'.zip')
 if path.exists():b=path.read_bytes()
 else:b=get('actions/artifacts/'+str(a['id'])+'/zip');path.write_bytes(b)
 assert len(b)==a['size_in_bytes'];assert a['digest']=='sha256:'+sha(b)
 z=zipfile.ZipFile(io.BytesIO(b));info=z.infolist();assert len(info)<=256;assert sum(x.file_size for x in info)<=64<<20
 entries=[]
 assert len({x.filename for x in info})==len(info)
 for x in info:
  q=pathlib.PurePosixPath(x.filename);assert not q.is_absolute() and '..' not in q.parts and '\\' not in x.filename
  assert not stat.S_ISLNK(x.external_attr>>16) and not x.flag_bits&1 and x.file_size<=16<<20
  if x.is_dir():continue
  body=z.read(x)
  entries.append({'path':x.filename,'bytes':len(body),'compressed_bytes':x.compress_size,'crc32':format(x.CRC,'08x'),'sha256':sha(body)})
 assert z.testzip() is None
 receipt={'id':a['id'],'name':name,'state':'verified','zip_bytes':len(b),'zip_sha256':sha(b),'declared_digest':a['digest'],'full_size_sha_crc_valid':True,'members':entries}
 (R/'verified-downloads'/(str(a['id'])+'-receipt.json')).write_text(json.dumps(receipt,indent=2)+'\n')
 return receipt
jobs=[j for f in p.glob('jobs-*.json') for j in json.loads(f.read_text())['jobs'] if j['status']=='completed' and j['steps']]
arts=[a for f in p.glob('artifacts-*.json') for a in json.loads(f.read_text())['artifacts']]
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as ex:logs=list(ex.map(log,jobs))
with concurrent.futures.ThreadPoolExecutor(max_workers=3) as ex:artifacts=list(ex.map(artifact,arts))
receipt={'snapshot':str(p),'logs':logs,'artifacts':artifacts}
(R/('collected-'+p.name+'.json')).write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps({'logs':[(x['id'],x['name'],x['bytes']) for x in logs],'artifacts':[{'id':x['id'],'state':x['state'],'members':[m['path'] for m in x.get('members',[])]} for x in artifacts]}))
