import hashlib,json,pathlib,subprocess,datetime
root=pathlib.Path(__file__).resolve().parent
snap=sorted((root/'snapshots').iterdir())[-1]
out=root/'logs';out.mkdir(exist_ok=True)
records=[]
for p in snap.glob('*-jobs.json'):
 for j in json.loads(p.read_text())['jobs']:
  if j['status']!='completed' or not j.get('steps'):continue
  path=out/('job-'+str(j['id'])+'.log')
  if not path.exists():
   proc=subprocess.run(['gh','api','repos/WuKongIM/packages/actions/jobs/'+str(j['id'])+'/logs'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=120)
   if proc.returncode:raise RuntimeError('job log download failed '+str(j['id']))
   assert len(proc.stdout)<=10*1024*1024
   path.write_bytes(proc.stdout)
  b=path.read_bytes()
  records.append({'job_id':j['id'],'name':j['name'],'conclusion':j['conclusion'],'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest(),'path':path.relative_to(root).as_posix()})
(root/'job-log-downloads.json').write_text(json.dumps({'observed_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'logs':records},indent=2)+'\n')
print(json.dumps(records))
