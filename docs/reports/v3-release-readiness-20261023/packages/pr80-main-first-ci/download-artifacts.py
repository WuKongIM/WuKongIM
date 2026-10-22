import concurrent.futures, datetime, hashlib, json, pathlib, subprocess, zipfile
ROOT=pathlib.Path(__file__).resolve().parent
snap=sorted((ROOT/'snapshots').iterdir())[-1]
out=ROOT/'artifacts';out.mkdir(exist_ok=True)
HEAD='e0982275c158b5bd69520c42719ce6f833ee63bc'
artifacts=[]
for p in snap.glob('*-artifacts.json'):
 d=json.loads(p.read_text()); assert len(d['artifacts'])==d['total_count']
 artifacts.extend(d['artifacts'])
def download(a):
 assert a['workflow_run']['head_sha']==HEAD
 receipt=out/(str(a['id'])+'-validation.json')
 if receipt.exists():return {'id':a['id'],'state':'previously verified'}
 if a['size_in_bytes']>10*1024*1024:return {'id':a['id'],'state':'not downloaded: larger than 10 MiB bound'}
 raw=subprocess.check_output(['gh','api','repos/WuKongIM/packages/actions/artifacts/'+str(a['id'])+'/zip'],timeout=90)
 assert len(raw)<10*1024*1024
 actual=hashlib.sha256(raw).hexdigest();assert a['digest']=='sha256:'+actual
 name=str(a['id'])+'-'+a['name'];assert '/' not in name and '\\' not in name
 p=out/(name+'.zip');p.write_bytes(raw)
 members=[]
 with zipfile.ZipFile(p) as z:
  infos=z.infolist();assert len(infos)<=500 and sum(i.file_size for i in infos)<100*1024*1024
  assert z.testzip() is None
  for i in infos:
   path=pathlib.PurePosixPath(i.filename)
   assert '..' not in path.parts and not path.is_absolute() and not i.is_dir()
   assert i.file_size<8*1024*1024 and path.suffix in ('.json','.jsonl','.txt','.log')
   b=z.read(i);target=out/name/path;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(b)
   members.append({'name':i.filename,'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest(),'crc32':f'{i.CRC:08x}'})
 data={'artifact_api':a,'downloaded_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'downloaded_bytes':len(raw),'local_sha256':actual,'api_sha256_matched':True,'zip_crc_verified':True,'members':members}
 receipt.write_text(json.dumps(data,indent=2)+'\n')
 return {'id':a['id'],'name':a['name'],'bytes':len(raw),'members':len(members),'state':'verified'}
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
 for result in pool.map(download,artifacts):print(json.dumps(result))
