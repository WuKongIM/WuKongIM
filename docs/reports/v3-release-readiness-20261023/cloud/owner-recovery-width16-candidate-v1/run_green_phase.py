import pathlib,json,hashlib,os,subprocess,selectors,time,signal,datetime,sys
b=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/owner-recovery-width16-candidate-v1')
w=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-owner-recovery-width16')
tests=json.loads(pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/owner-recovery-width16-tests-first-v1/tests-first-pins.json').read_text())['files']
p=json.loads((b/'green-execution-plan.json').read_text())
def sha(f): return hashlib.sha256(f.read_bytes()).hexdigest()
for name,v in tests.items(): assert sha(w/name)==v['sha256'] and (w/name).stat().st_size==v['bytes'],name
frozen=json.loads(pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/owner-recovery-width16-tests-first-v1/frozen-source.json').read_text())['files']
source={name:sha(w/name) for name in frozen}
source.update({name:sha(w/name) for name in ['CHANGELOG.md','docs/development/PROJECT_KNOWLEDGE.md']})
pins=b/'candidate-source-pins.json'
if not pins.exists(): pins.write_text(json.dumps(source,indent=2)+'\n')
assert json.loads(pins.read_text())==source
for name,v in frozen.items():
 if name not in ['internal/usecase/presence/recovery_flights.go','internal/infra/cluster/presence_recovery.go','internal/usecase/presence/FLOW.md','internal/infra/cluster/FLOW.md']: assert source[name]==v['sha256'],name
for index in map(int,sys.argv[1:]):
 c=p['commands'][index]; phase=c['phase'];print('START '+phase,flush=True)
 env={'PATH':str(pathlib.Path(c['argv'][0]).parent)+':/usr/bin:/bin:/usr/sbin:/sbin','HOME':os.environ['HOME'],'GOPATH':'/Users/tt/go','GOWORK':'off','GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOMAXPROCS':'2','GOFLAGS':'-p=1'}
 if 'TMPDIR' in os.environ:env['TMPDIR']=os.environ['TMPDIR']
 out=b/'green-results';out.mkdir(exist_ok=True)
 assert not (out/(phase+'.result.json')).exists()
 started=datetime.datetime.now(datetime.timezone.utc).isoformat();t0=time.monotonic();proc=subprocess.Popen(c['argv'],cwd=w,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
 sel=selectors.DefaultSelector();streams={};size={'stdout':0,'stderr':0};limits={'stdout':c['stdout_bytes_limit'],'stderr':c['stderr_bytes_limit']};killed=None
 for name,pipe in [('stdout',proc.stdout),('stderr',proc.stderr)]:
  os.set_blocking(pipe.fileno(),False);sel.register(pipe,selectors.EVENT_READ,name);streams[name]=open(out/(phase+'.'+name),'xb')
 while sel.get_map():
  if killed is None and time.monotonic()-t0>c['outer_seconds']:
   killed='outer_timeout';os.killpg(proc.pid,signal.SIGKILL)
  for key,_ in sel.select(.1):
   data=os.read(key.fileobj.fileno(),65536);name=key.data
   if not data:sel.unregister(key.fileobj);continue
   remain=max(0,limits[name]-size[name]);streams[name].write(data[:remain]);size[name]+=len(data)
   if size[name]>limits[name] and killed is None:killed=name+'_cap';os.killpg(proc.pid,signal.SIGKILL)
 rc=proc.wait()
 for f in streams.values():f.close()
 for name,v in tests.items():assert sha(w/name)==v['sha256'],name
 for name,v in source.items():assert sha(w/name)==v,name
 events=[];outputs=[]
 for line in (out/(phase+'.stdout')).read_text(errors='replace').splitlines():
  try:e=json.loads(line)
  except ValueError:continue
  if e.get('Action') in ['pass','fail','skip']:events.append({k:e[k] for k in ('Action','Package','Test','Elapsed') if k in e})
  if e.get('Output') and any(s in e['Output'] for s in ['.go:', 'build failed','FAIL','panic:']):outputs.append(e['Output'].strip())
 r={'phase':phase,'argv':c['argv'],'started_utc':started,'elapsed_seconds':round(time.monotonic()-t0,3),'exit_code':rc,'outer_or_output_kill':killed,'pgid':proc.pid,'output_bytes':size,'event_count':len(events),'events':events,'bounded_diagnostics':outputs[-20:],'frozen_test_and_candidate_source_hashes_pre_post_verified':True}
 (out/(phase+'.result.json')).write_text(json.dumps(r,indent=2)+'\n')
 print('DONE '+json.dumps({k:v for k,v in r.items() if k!='events'}),flush=True)
 if rc!=0:print('STOP_FIRST_FAILURE',flush=True);sys.exit(1)
print('PHASES_DONE',flush=True)
