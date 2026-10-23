from pathlib import Path
import os,sys,json,subprocess,hashlib,datetime,time,collections
O=Path(__file__).resolve().parent
R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-metrics-fetch-diagnostics')
TOOL=Path('/Users/tt/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.11.darwin-arm64/bin')
name=sys.argv[1]
settings={'PATH':str(TOOL)+':'+os.environ['PATH'],'GOWORK':'off','GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOMAXPROCS':'2','CGO_ENABLED':'0','GOFLAGS':'-p=1'}
policy_path=R/'.github/review-agent/policy.json';policy=json.loads(policy_path.read_text());policy_hash=hashlib.sha256(policy_path.read_bytes()).hexdigest()
assert policy_hash=='2ee1b3162b431d77bec8cf68acc5b8aec0eede735f03c9f89d6c42e1d86cb0f9'
max_bytes=16<<20;timeout=300;declared=None
if name=='race':
 settings['CGO_ENABLED']='1'
 argv=[str(TOOL/'go'),'test','-race','./internal/bench/target','./internal/bench/chatlifecycle','./cmd/wkcli/internal/benchmark','-run','^Test(Metrics|MetaCheckpointFailureLeaves)','-count=1','-timeout=120s','-json']
elif name=='full':
 argv=[str(TOOL/'go'),'test','./internal/bench/target','./internal/bench/chatlifecycle','./cmd/wkcli/...','-count=1','-timeout=180s','-json']
elif name=='flow-render':
 argv=[str(TOOL/'go'),'run','./scripts/flowcheck','--mode','render','--write-index'];timeout=60;max_bytes=1048576
elif name in ['go-format','flow-doc-contracts']:
 declared=policy['trusted_checks'][name];timeout=declared['timeout_seconds'];max_bytes=declared['max_output_bytes'];args=declared['arguments']
 if args[0]=='review-agent-check':argv=[str(TOOL/'go'),'run','./cmd/wkreviewcheck',*args[1:]]
 else:assert args[0]=='go';argv=[str(TOOL/'go'),*args[1:]]
else:raise ValueError('unknown fixed check')
assert not (O/f'{name}-receipt.json').exists()
tests=json.loads((O/'tests-first-hashes-v2.json').read_text())['test_files']
assert all(hashlib.sha256((R/n).read_bytes()).hexdigest()==h for n,h in tests.items())
start=datetime.datetime.now(datetime.timezone.utc).isoformat();at=time.monotonic()
p=subprocess.run(argv,cwd=R,env={**os.environ,**settings},stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=timeout)
assert len(p.stdout)+len(p.stderr)<=max_bytes
(O/f'{name}.stdout').write_bytes(p.stdout);(O/f'{name}.stderr').write_bytes(p.stderr)
counts=collections.Counter();top=collections.Counter();packages={};failures=[]
if name in ['full','race']:
 for ln in p.stdout.splitlines():
  try:d=json.loads(ln)
  except:continue
  if d.get('Action')in ['pass','fail','skip']:
   if 'Test'in d:
    counts[d['Action']]+=1
    if '/'not in d['Test']:top[d['Action']]+=1
    if d['Action']=='fail':failures.append(d['Test'])
   else:packages[d.get('Package','')]=d['Action']
receipt={'name':name,'argv':argv,'cwd':str(R),'environment':settings,'started_at_utc':start,'elapsed_seconds':time.monotonic()-at,'exit_status':p.returncode,'timeout_seconds':timeout,'max_output_bytes':max_bytes,'stdout_bytes':len(p.stdout),'stderr_bytes':len(p.stderr),'test_events':dict(counts),'top_level_tests':dict(top),'packages':packages,'failure_tests':failures,'named_check_policy_entry':declared,'policy_sha256':policy_hash,'tests_unchanged':all(hashlib.sha256((R/n).read_bytes()).hexdigest()==h for n,h in tests.items())}
(O/f'{name}-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps({k:receipt[k] for k in ['name','exit_status','elapsed_seconds','test_events','top_level_tests','packages','stdout_bytes','stderr_bytes','tests_unchanged']},indent=2))
if p.returncode:print(p.stderr.decode()[:1800])
