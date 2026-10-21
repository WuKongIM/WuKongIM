from pathlib import Path,PurePosixPath
import subprocess,json,hashlib,os,zipfile,re,stat,datetime
P=Path(__file__).resolve().parent;D=P.parent/'pr75-ci-generation-2-private';snap=P/(P/'latest-snapshot.txt').read_text().strip()
sha=lambda b:hashlib.sha256(b).hexdigest()
for a in json.loads((snap/'39636512103-artifacts.json').read_text())['artifacts']:
 name=a['name'];out=P/'artifacts'/name;out.mkdir(parents=True,exist_ok=True)
 if (out/'download-receipt.json').exists():continue
 assert a['size_in_bytes']<32*1024*1024 and not a['expired']
 raw=D/(str(a['id'])+'.zip');err=D/(str(a['id'])+'.stderr');assert not raw.exists()
 with raw.open('xb') as f,err.open('xb') as e:
  os.chmod(raw,0o600);os.chmod(err,0o600)
  try:r=subprocess.run(['gh','api','--method','GET',f"repos/WuKongIM/packages/actions/artifacts/{a['id']}/zip"],stdout=f,stderr=e,timeout=120);code=r.returncode
  except subprocess.TimeoutExpired:code=124
 eb=err.read_bytes();b=raw.read_bytes();rec={'artifact':a,'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'exit_code':code,'raw_stderr_sha256':sha(eb),'raw_stderr_bytes':len(eb),'downloaded_bytes':len(b),'downloaded_sha256':sha(b)}
 if code!=0:
  (out/'first-download-failure.json').write_text(json.dumps(rec,indent=2)+'\n');print('GET FAILED',name,flush=True);continue
 assert len(b)==a['size_in_bytes'] and a['digest']=='sha256:'+sha(b)
 inventory=[]
 with zipfile.ZipFile(raw) as z:
  infos=z.infolist();assert len(infos)<100 and sum(i.file_size for i in infos)<64*1024*1024 and len({i.filename for i in infos})==len(infos)
  assert z.testzip() is None
  for i in infos:
   path=PurePosixPath(i.filename);assert not path.is_absolute() and '..' not in path.parts and not stat.S_ISLNK(i.external_attr>>16)
   item={'path':i.filename,'size':i.file_size,'crc32':format(i.CRC,'08x'),'retained':False}
   if not i.is_dir() and path.suffix in ('.json','.jsonl'):
    content=z.read(i);assert len(content)<32*1024*1024
    assert not re.search(rb'-----BEGIN (?:OPENSSH |RSA |EC |DSA )?PRIVATE KEY|github_pat_[A-Za-z0-9_]+|gh[pousr]_[A-Za-z0-9]{30,}|https?://[^\s\"]+[?&](?:sig|token|signature|X-Amz-Signature)=',content,re.I)
    if path.suffix=='.json':json.loads(content)
    else:
     for line in content.splitlines():json.loads(line)
    dest=out/path;dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(content);item.update(retained=True,sha256=sha(content))
   inventory.append(item)
 rec.update(api_digest_verified=True,all_zip_crc_verified=True,members=inventory)
 (out/'download-receipt.json').write_text(json.dumps(rec,indent=2)+'\n');print(name,len(b),len(inventory),flush=True)
