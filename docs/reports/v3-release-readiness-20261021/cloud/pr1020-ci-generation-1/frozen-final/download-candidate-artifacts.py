import pathlib, subprocess, json, hashlib, zipfile, stat, datetime
root=pathlib.Path(__file__).resolve().parent
out=root/'candidate-artifacts';out.mkdir(exist_ok=True)
run=39598983439
api=json.loads(subprocess.check_output(['gh','api',f'repos/WuKongIM/WuKongIM/actions/runs/{run}/artifacts?per_page=100']))
(out/'latest-api.json').write_text(json.dumps(api,indent=2)+'\n')
for a in api['artifacts']:
 if a['name'] not in [f'v3-candidate-{part}-{run}-1' for part in ['scripts','default','e2e']]:continue
 receipt=out/f"artifact-{a['id']}.json"
 if receipt.exists():continue
 assert not a['expired'] and a['workflow_run']['id']==run and a['workflow_run']['head_sha']=='0cdfaa37b00c58819895dee54029c27c83c9caa9' and a['workflow_run']['repository_id']==638054601 and a['workflow_run']['head_repository_id']==638054601
 assert a['size_in_bytes']<150*1024*1024
 z=out/f"artifact-{a['id']}.zip"
 with z.open('xb') as f:
  p=subprocess.run(['gh','api',f"repos/WuKongIM/WuKongIM/actions/artifacts/{a['id']}/zip"],stdout=f,stderr=subprocess.PIPE,timeout=180)
 assert p.returncode==0, "read-only artifact download failed; raw URL stderr withheld"
 h=hashlib.sha256(z.read_bytes()).hexdigest()
 assert z.stat().st_size==a['size_in_bytes']
 assert a['digest']=='sha256:'+h
 dest=out/f"artifact-{a['id']}";dest.mkdir()
 with zipfile.ZipFile(z) as f:
  assert len(f.infolist()) < 1000
  assert len({i.filename for i in f.infolist()}) == len(f.infolist())
  assert sum(i.file_size for i in f.infolist()) < 256*1024*1024
  assert f.testzip() is None
  for i in f.infolist():
   q=pathlib.PurePosixPath(i.filename)
   assert not q.is_absolute() and '..' not in q.parts
   assert not stat.S_ISLNK(i.external_attr>>16)
  f.extractall(dest)
 record={'artifact':a,'observed_sha256':h,'observed_size':z.stat().st_size,'zip_crc_pass':True,'downloaded_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'directory':str(dest)}
 receipt.write_text(json.dumps(record,indent=2)+'\n');print(json.dumps({'artifact_id':a['id'],'name':a['name'],'size':z.stat().st_size,'sha256':h,'directory':str(dest)}),flush=True)
