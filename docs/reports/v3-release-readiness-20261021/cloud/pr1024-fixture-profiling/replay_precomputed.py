import sys
sys.dont_write_bytecode=True
from pathlib import Path
import json,importlib.util,time,subprocess,shutil,os,hashlib,copy,re,shlex
ROOT=Path(sys.argv[2]).resolve()
D=Path(sys.argv[1]).resolve(); D.mkdir(parents=True,exist_ok=False)
AUDIT=Path(__file__).resolve().parent
freeze=json.loads((AUDIT/'source-freeze.json').read_text())
for path,sha in freeze['files'].items():
 assert hashlib.sha256((ROOT/path).read_bytes()).hexdigest()==sha, 'source drift: '+path
(D/'bin').mkdir()
for tool in ['ssh','timeout','python3','jq','wc','tr','install','rm','mktemp','date','head']:
 (D/'bin'/tool).symlink_to(AUDIT/'trace-tool')
spec=importlib.util.spec_from_file_location('fixture',ROOT/'scripts/testdata/chat_lifecycle_terminal_evidence_test.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
bin=D/'bin';names=['ssh','timeout','python3','jq','wc','tr','install','rm','mktemp','date','head']
if not (bin/'head').exists():(bin/'head').symlink_to(D/'trace-tool')
original_run=subprocess.run;current={}
METRIC_NAMES={'presence':'wukongim_presence_endpoint_lookup_total','delivery':'wukongim_delivery_recipient_worker_process_total',**m.NAMES}
def classify(command):
 if 'worker-evidence.json' in command or 'diagnostic-status.json' in command:
  filename='worker-evidence.json' if 'worker-evidence.json' in command else 'diagnostic-status.json';stage=re.search(r'/reports/(rehearsal|formal|capacity)/',command).group(1);return {'kind':stage+'/'+filename}
 if 'api/v1/query_range' not in command:return {'kind':'targets' if 'api/v1/targets' in command else 'other'}
 words=shlex.split(command)
 if words[:4]==['bash','-o','pipefail','-c']:words=shlex.split(words[4])
 params=dict(words[i+1].split('=',1) for i,w in enumerate(words) if w=='--data-urlencode');query=params['query']
 family=next((f for part,f in [('presence_endpoint_lookup_','presence'),('delivery_','delivery'),('runtime_','runtime'),('channel_worker_','channel_worker'),('process_','process'),('metadata_create_total','counters'),('go_','go')] if part in query),None)
 if query in ('up','{__name__="up"}'):family='up'
 assert family is not None
 return dict(kind='range-'+family,query=query,**{key:params[key] for key in ['start','end','step']})
def prepare(env):
 cfg=json.loads(Path(env['WK_TEST_CONFIG']).read_text());fake=Path(shutil.which('ssh',path=env['PATH']));prepared=fake.parent/'prepared';prepared.mkdir();raw=fake.parent/'actual-requests';env['PROFILE_RAW_CALLS']=str(raw);env['PROFILE_RESPONSES']=str(prepared)
 choices=[];codes={}
 for key,path in cfg['files'].items():
  mode=cfg['document_modes'].get(key,'normal');body=Path(path).read_bytes();code=0
  if mode=='missing':code=3;body=b''
  elif mode!='normal':raise AssertionError('prototype only measures declared document modes')
  kind=key.replace('/','-');(prepared/kind).write_bytes(body);codes[kind]=code;choices.append('*'+key+'*) kind='+kind+' ;;')
 for family in m.FAMILIES:
  mode=cfg['metric_modes'].get(family,'normal');series={'metric':{'__name__':METRIC_NAMES[family]},'values':[[1792602410,'1']]}
  if mode=='large':series['metric']['fixture_padding']='x'*900000
  elif mode!='normal':raise AssertionError('prototype only measures declared metric modes')
  body=json.dumps({'status':'success','data':{'resultType':'matrix','result':[series]}}).encode();kind='range-'+family;(prepared/kind).write_bytes(body);codes[kind]=0
 script='''#!/bin/sh
command=""
for arg do command="$arg"; done
printf '%s\\0' "$command" >>"$PROFILE_RAW_CALLS"
case "$command" in
'''+ '\n'.join(choices)+'''
 *api/v1/query_range*)
  case "$command" in
   *presence_endpoint_lookup_*) kind=range-presence ;;
   *delivery_*) kind=range-delivery ;;
   *runtime_*) kind=range-runtime ;;
   *channel_worker_*) kind=range-channel_worker ;;
   *process_*) kind=range-process ;;
   *metadata_create_total*) kind=range-counters ;;
   *go_*) kind=range-go ;;
   *query=up*) kind=range-up ;;
   *) exit 99 ;;
  esac ;;
 *api/v1/targets*) printf '%s' '{"status":"success","data":{"activeTargets":[]}}'; exit 0 ;;
 *) printf 'fixture terminal capture\\n'; exit 0 ;;
esac
case "$kind" in
'''+ '\n'.join(key+') code='+str(value)+' ;;' for key,value in codes.items())+'''
 *) exit 99 ;;
esac
if [ "$code" -eq 3 ]; then exit 3; fi
rest=${command##*head -c }
limit=${rest%%[!0-9]*}
case "$limit" in ''|*[!0-9]*) exit 98 ;; esac
head -c "$limit" "$PROFILE_RESPONSES/$kind"
exit "$code"
'''
 fake.write_text(script);fake.chmod(0o700);return cfg,raw

def traced_run(command,*args,**kwargs):
 env=dict(kwargs['env']);cfg,raw=prepare(env);original_path=env['PATH'];env['PROFILE_PHASE_LOG']=str(current['trace'])
 for name in names:env['PROFILE_REAL_'+name]=shutil.which(name,path=original_path)
 env['PATH']=str(bin)+os.pathsep+original_path;kwargs['env']=env
 start=time.monotonic();result=original_run(command,*args,**kwargs);current['collector_seconds']=time.monotonic()-start
 calls=[classify(c.decode()) for c in raw.read_bytes().split(b'\0') if c]
 Path(cfg['calls']).write_text(''.join(json.dumps(c)+'\n' for c in calls));return result
m.subprocess.run=traced_run
baseline=json.loads((AUDIT.parent/'rc-new-cee5/terminal-evidence-repair-v2/focused-corrected-results.json').read_text());by_case={x['case']:x for x in baseline['cases']}
cases=[('normal-corrected','rehearsal',{}),('large','large-split',{'metric_modes':dict.fromkeys(m.FAMILIES,'large')}),('missing-worker','worker-missing',{'document_modes':{'rehearsal/worker-evidence.json':'missing'}}),('invalid-anchor','anchor-missing-end',{'report_mutation':lambda x:x['window'].pop('end')}),('formal-capacity','capacity',{'stage':'formal','capacity':True})]
results=[]
try:
 for name,base,args in cases:
  current={'trace':D/(name+'-precomputed-corrected-trace.jsonl')};start=time.monotonic();rc,manifest,calls,retained,files=m.CollectorTests().run_case(name,**args);wall=time.monotonic()-start
  before=copy.deepcopy(by_case[base]);after=copy.deepcopy(m.RESULTS[-1]);normalization=[]
  for side,c in [('before',before),('after',after)]:
   raw=(json.dumps(c['manifest'],indent=2,ensure_ascii=False)+'\n').encode();entry=next(x for x in c['files'] if x['path']=='manifest.json');assert len(raw)==entry['bytes'];assert hashlib.sha256(raw).hexdigest()==entry['sha256']
   stamp=c['manifest']['observed_at'];assert re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ',stamp)
   c['manifest']['observed_at']='2000-01-01T00:00:00Z';canonical=(json.dumps(c['manifest'],indent=2,ensure_ascii=False)+'\n').encode();canonical_sha=hashlib.sha256(canonical).hexdigest();normalization.append(dict(side=side,observed_at=stamp,raw_manifest_sha256=entry['sha256'],canonical_manifest_sha256=canonical_sha));entry['sha256']=canonical_sha
  del before['case'];del after['case'];equivalent=before==after
  trace=[json.loads(s) for s in current['trace'].read_text().splitlines()];groups={tool:{'count':sum(e['tool']==tool for e in trace),'inclusive_seconds':sum(e['duration'] for e in trace if e['tool']==tool)} for tool in names}
  result={'case':name,'baseline_case':base,'fixture_seconds':wall,'collector_seconds':current['collector_seconds'],'collector_exit':rc,'complete':manifest['complete'],'tools':groups,'strict_semantic_equivalent':equivalent,'normalization':normalization,'actual':m.RESULTS[-1]};results.append(result);(D/'precomputed-corrected-results.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps({k:v for k,v in result.items() if k not in ['actual','normalization']}),flush=True)
  assert equivalent,(name,before,after)
finally:m.subprocess.run=original_run
