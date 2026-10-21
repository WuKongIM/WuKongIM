import pathlib,subprocess,json,hashlib,zipfile,stat,datetime,re,re
root=pathlib.Path(__file__).resolve().parent;out=root/'chat-artifacts';out.mkdir(exist_ok=True);data=json.loads((root/'39598983277-artifacts-01.json').read_text())
for a in data['artifacts']:
 receipt=out/f"artifact-{a['id']}.json"
 if receipt.exists():continue
 assert not a['expired'] and a['size_in_bytes']<300*1024*1024 and a['workflow_run']['id']==39598983277 and a['workflow_run']['head_sha']=='0cdfaa37b00c58819895dee54029c27c83c9caa9' and a['workflow_run']['repository_id']==638054601 and a['workflow_run']['head_repository_id']==638054601
 z=out/f"artifact-{a['id']}.zip"
 with z.open('xb') as f:r=subprocess.run(['gh','api',f"repos/WuKongIM/WuKongIM/actions/artifacts/{a['id']}/zip"],stdout=f,stderr=subprocess.PIPE,timeout=240)
 assert r.returncode==0,re.sub(r'https://[^\s\"\']+', '<redacted-url>', r.stderr.decode());digest=hashlib.file_digest(z.open('rb'),'sha256').hexdigest();assert a['digest']=='sha256:'+digest and z.stat().st_size==a['size_in_bytes']
 with zipfile.ZipFile(z) as f:
  entries=f.infolist();assert len(entries)<5000 and len({x.filename for x in entries})==len(entries) and sum(x.file_size for x in entries)<2*1024**3
  for i in entries:
   p=pathlib.PurePosixPath(i.filename);assert not p.is_absolute() and '..' not in p.parts and not stat.S_ISLNK(i.external_attr>>16)
  assert f.testzip() is None
  manifest=[{'path':i.filename,'bytes':i.file_size,'compressed_bytes':i.compress_size} for i in entries]
  (out/f"artifact-{a['id']}-entries.json").write_text(json.dumps(manifest,indent=2)+'\n')
 record={'artifact':a,'observed_sha256':digest,'observed_bytes':z.stat().st_size,'zip_crc_pass':True,'entry_count':len(entries),'uncompressed_bytes':sum(x.file_size for x in entries),'at':datetime.datetime.now(datetime.timezone.utc).isoformat()};receipt.write_text(json.dumps(record,indent=2)+'\n');print(json.dumps({'artifact':a['name'],'bytes':a['size_in_bytes'],'verified':True,'entries':len(entries),'reports':[x['path'] for x in manifest if x['path'].endswith('.json')][:30]}),flush=True)
