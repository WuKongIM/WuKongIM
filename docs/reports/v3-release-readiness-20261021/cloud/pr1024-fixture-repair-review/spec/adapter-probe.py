from pathlib import Path
import sys,importlib.util,tempfile,os,subprocess,json,shlex,hashlib
sys.dont_write_bytecode=True
root=Path(sys.argv[1]);s=importlib.util.spec_from_file_location('fixture',root/'scripts/testdata/chat_lifecycle_terminal_evidence_test.py');f=importlib.util.module_from_spec(s);s.loader.exec_module(f)
results=[]
def metric(query='{__name__=~"wukongim_runtime_.*"}',start='1792601510.121',end='1792602410.121',step='15',limit=512):
 inner='curl --get --fail --silent --show-error --max-time 10'+''.join(' --data-urlencode '+shlex.quote(k+'='+v) for k,v in [('query',query),('start',start),('end',end),('step',step)])+' http://127.0.0.1:9090/api/v1/query_range | head -c '+str(limit)
 return 'bash -o pipefail -c '+shlex.quote(inner)
with tempfile.TemporaryDirectory(prefix='spec-adapter-') as tmp:
 d=Path(tmp); files={}
 for stage in ['rehearsal','formal','capacity']:
  for name in ['worker-evidence.json','diagnostic-status.json']:
   p=d/(stage+'-'+name);p.write_bytes(b'{"marker":"'+stage.encode()+b'-'+name.encode()+b'"}');files[stage+'/'+name]=str(p)
 modes={'rehearsal/worker-evidence.json':'partial','rehearsal/diagnostic-status.json':'unavailable','formal/worker-evidence.json':'missing','formal/diagnostic-status.json':'timeout','capacity/worker-evidence.json':'normal','capacity/diagnostic-status.json':'malformed'}
 cfg={'files':files,'document_modes':modes,'metric_modes':{'runtime':'boundary-start'},'combined_oversized':False}
 adapter=d/'ssh';adapter.write_text(f.prepare_ssh(d,cfg,f.END));adapter.chmod(0o700);env=dict(os.environ,TERMINAL_FIXTURE_COMMANDS=str(d/'commands'),TERMINAL_FIXTURE_RESPONSES=str(d/'responses'))
 def run(label,command,expected_code,expected_body=None,stderr_canary=False,parse=True):
  before=(d/'commands').read_bytes() if (d/'commands').exists() else b''
  r=subprocess.run([str(adapter),'-F','unused','fixture-host',command],env=env,capture_output=True,timeout=5)
  records=(d/'commands').read_bytes();assert records==before+command.encode()+b'\0'
  if parse: calls=f.ssh_calls(d/'commands')
  else:calls=[]
  ok=r.returncode==expected_code and ((expected_body is None) or r.stdout==expected_body) and ((b'secret-canary-do-not-publish' in r.stderr)==stderr_canary)
  results.append({'case':label,'actual_command':command,'exit_code':r.returncode,'expected_exit_code':expected_code,'stdout_bytes':len(r.stdout),'stdout_sha256':hashlib.sha256(r.stdout).hexdigest(),'stderr_canary':b'secret-canary-do-not-publish' in r.stderr,'actual_command_recorded':True,'last_parsed_call':calls[-1] if calls else None,'pass':ok});assert ok,label
  return r
 good=run('boundary-actual-valid',metric(),0);assert json.loads(good.stdout)['data']['result'][0]['values'][0][0]==1792601510.121
 run('boundary-wrong-start',metric(start='1792601510.122'),99,b'')
 run('boundary-wrong-end',metric(end='1792602410.122'),99,b'')
 run('boundary-wrong-step',metric(step='30'),99,b'')
 run('boundary-actual-clip',metric(limit=7),0,good.stdout[:7])
 run('dispatch-up',metric(query='up'),0)
 run('dispatch-go',metric(query='{__name__=~"wukongim_go_.*"}'),0)
 for key,code,canary in [('rehearsal/worker-evidence.json',255,False),('rehearsal/diagnostic-status.json',255,True),('formal/worker-evidence.json',3,False),('formal/diagnostic-status.json',124,False),('capacity/worker-evidence.json',0,False),('capacity/diagnostic-status.json',0,False)]:
  raw=('cat /reports/'+key+' | head -c 13');mode=modes[key];body=b'' if mode in ('unavailable','missing','timeout') else b'not-json-secret-canary-do-not-publish'[:13] if mode=='malformed' else Path(files[key]).read_bytes()[:13];run('document-'+mode,raw,code,body,canary)
 run('unknown-query',metric(query='not_a_metric'),99,b'',parse=False)
 try:f.ssh_calls(d/'commands'); unknown_rejected=False
 except AssertionError:unknown_rejected=True
 results.append({'case':'actual-unknown-query-record-rejected','pass':unknown_rejected});assert unknown_rejected
print(json.dumps({'scope':'Direct temporary shell SSH adapter only; no collector cases, product, network, CI or full suite.','count':len(results),'failed':sum(not x['pass'] for x in results),'results':results},indent=2))
