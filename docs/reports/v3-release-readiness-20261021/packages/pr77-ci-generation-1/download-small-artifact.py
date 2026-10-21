from pathlib import Path,PurePosixPath
import hashlib,json,os,re,stat,subprocess,sys,zipfile,zlib,datetime
P=Path(__file__).resolve().parent; D=P.parent/'pr77-ci-generation-1-private'
a=json.loads(Path(sys.argv[1]).read_bytes()); aid=int(sys.argv[2]); item=next(x for x in a['artifacts'] if x['id']==aid)
assert item['size_in_bytes']<=16*1024*1024 and not item['expired']
name=item['name'];assert re.fullmatch('[a-z0-9-]+',name)
zip_path=D/(str(aid)+'.zip');err=D/(str(aid)+'.stderr');assert not zip_path.exists()
with zip_path.open('xb') as out,err.open('xb') as e:
 os.chmod(zip_path,0o600);os.chmod(err,0o600)
 try:code=subprocess.run(['gh','api','--method','GET',f'repos/WuKongIM/packages/actions/artifacts/{aid}/zip'],stdout=out,stderr=e,timeout=180).returncode
 except subprocess.TimeoutExpired:code=124
raw=zip_path.read_bytes(); eb=err.read_bytes(); sha=lambda b:hashlib.sha256(b).hexdigest()
rec={'artifact':item,'get_exit_code':code,'zip_size':len(raw),'zip_sha256_recomputed':sha(raw),'stderr_size':len(eb),'stderr_sha256':sha(eb),'all_members_crc_verified':False,'members':[]}
p=P/'artifacts'/name;p.mkdir(parents=True,exist_ok=True)
if code!=0:
 (p/'download-failure.json').write_text(json.dumps(rec,indent=2)+'\n');print(name,'GET_failed',code);raise SystemExit(1)
assert len(raw)==item['size_in_bytes'];assert item.get('digest')=='sha256:'+sha(raw)
with zipfile.ZipFile(zip_path) as z:
 infos=z.infolist();assert len(infos)<=32 and sum(i.file_size for i in infos)<=24*1024*1024
 assert len({i.filename for i in infos})==len(infos)
 for i in infos:
  q=PurePosixPath(i.filename);assert not q.is_absolute() and '..' not in q.parts and '\\' not in i.filename
  assert not stat.S_ISLNK(i.external_attr>>16) and not i.is_dir() and i.file_size<=10*1024*1024
  b=z.read(i);assert zlib.crc32(b)&0xffffffff==i.CRC
  assert q.suffix in ['.json','.jsonl'],i.filename
  assert not re.search(rb'-----BEGIN (?:PGP PRIVATE KEY BLOCK|(?:OPENSSH |RSA |EC |DSA )?PRIVATE KEY)|github_pat_[A-Za-z0-9_]+|gh[pousr]_[A-Za-z0-9]{30,}',b)
  if q.suffix=='.json':json.loads(b)
  else:
   for line in b.splitlines():json.loads(line)
  dest=p/q;dest.parent.mkdir(parents=True,exist_ok=True);assert not dest.exists();dest.write_bytes(b)
  rec['members'].append({'path':i.filename,'size':len(b),'sha256':sha(b),'crc32':format(i.CRC,'08x'),'crc_verified':True})
rec['all_members_crc_verified']=True;rec['whole_zip_verified_against_api_digest']=True
(p/'download-receipt.json').write_text(json.dumps(rec,indent=2)+'\n');print(name,'verified',len(raw),len(rec['members']))
