#!/usr/bin/env python3
"""Credential-free probes of frozen shell functions; no cloud or product calls."""
import argparse,datetime,hashlib,json,os,re,signal,subprocess,time
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--source',type=Path,required=True);p.add_argument('--bash',type=Path,required=True);p.add_argument('--output',type=Path,required=True)
a=p.parse_args();src=a.source.resolve();out=a.output.resolve();out.mkdir(exist_ok=False)
files=['scripts/cloud-sim/analyze.sh','scripts/run-wukongim-three-node-chat-lifecycle-local-baseline.sh','scripts/bench-wukongim-single-node-1000ch.sh','scripts/wukongim_single_node_terminal_cut_integration_test.go']
source_sha=subprocess.check_output(['git','rev-parse','HEAD'],cwd=src,text=True).strip()
freeze={f:hashlib.sha256((src/f).read_bytes()).hexdigest() for f in files}
def function(body,name):
 m=re.search(r'(?ms)^'+re.escape(name)+r'\(\) \{\n.*?^\}',body);assert m,name;return m.group()
def write(path,body):path.write_text(body);path.chmod(0o700)
results=[]
def run(name,path,env=None,signal_ready=None):
 d={'name':name,'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':[str(a.bash),'-x',str(path)]}
 with (out/(name+'.log')).open('wb') as log:
  child=subprocess.Popen(d['command'],cwd=src,env=dict(env or os.environ.copy(),LC_ALL='C'),stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
  try:
   if signal_ready:
    limit=time.monotonic()+3
    while not signal_ready.exists() and child.poll() is None and time.monotonic()<limit:time.sleep(.005)
    d['ready_before_signal']=signal_ready.exists()
    if d['ready_before_signal']:child.send_signal(signal.SIGTERM)
   d['exit_code']=child.wait(timeout=8)
  except subprocess.TimeoutExpired:d['timeout']=True
  finally:
   try:os.killpg(child.pid,signal.SIGKILL)
   except ProcessLookupError:pass
   child.wait()
 d['finished_at']=datetime.datetime.now(datetime.timezone.utc).isoformat();results.append(d);return d
body=(src/files[0]).read_text()
for variant in ['original','bridge-return-zero','all-cleanup-returns-zero']:
 explicit=variant!='original'
 name='analysis-'+variant
 marker=out/(name+'.closed')
 functions='\n'.join(function(body,f) for f in ['dispatch_close_best_effort','stop_analysis_bridge','cleanup'])
 if explicit:
  old='if [[ -z "$analysis_bridge_pid" ]]; then\n    return\n  fi';assert old in functions
  functions=functions.replace(old,old.replace('return\n','return 0\n'),1)
 if variant=='all-cleanup-returns-zero':functions=functions.replace('\n    return\n','\n    return 0\n')
 script=out/(name+'.sh')
 temp=out/(name+'.temp');temp.mkdir();(temp/'owned.txt').write_text('fixture')
 done=out/(name+'.done')
 setup='''set -euo pipefail
analysis_bridge_pid="";analysis_bridge_url="";session_open=true;repository=fixture/project;request_id=fixture-request;run_id=fixture-run
close_attempted_request_id="";analysis_token="";analysis_worktree_added=false;analysis_worktree="";analysis_temp="";result_temp=""
'''
 setup+='analysis_temp='+str(temp)+'\n'
 stubs='dispatch_close_once() { printf "closed\\n" >> '+str(marker)+'; return 0; }\nwk_cleanup_local_cloud_tools() { printf "done\\n" >> '+str(done)+'; }\n'
 write(script,setup+stubs+functions+'\ntrap cleanup EXIT\nexit 17\n')
 d=run(name,script);d['close_marker']=marker.read_text() if marker.exists() else None;d['expected_original_exit_preserved']=d.get('exit_code')==17;d['close_attempted']=marker.exists();d['owned_temp_removed']=not temp.exists();d['cleanup_completed']=done.exists()
production=(src/files[2]).read_text();suffix='\nmain "$@"\n';assert production.endswith(suffix)
test=(src/files[3]).read_text();test=test[test.index('func TestSingleNodeTerminalObserverJoinSurvivesParentSignal'):]
match=re.search(r'harnessBody := strings.TrimSuffix\(production, entrypoint\) \+ `(.*?)`',test,re.S);assert match
script=out/'observer-original.sh';write(script,production[:-len(suffix)]+'\nset -x\n'+match.group(1))
env=os.environ.copy();env.update({'WK_TEST_STOP_FILE':str(out/'observer.stop'),'WK_TEST_OBSERVER_PID':str(out/'observer.pid')})
for index in range(20):
 for f in ['observer.stop','observer.pid']:
  (out/f).unlink(missing_ok=True)
 d=run('observer-original-%02d'%index,script,env);d['observer_receipt_present']=(out/'observer.pid').exists()
# Exactly the fake handshake order used by the existing integration fixture.
case=out/'baseline-fixture';(case/'scripts').mkdir(parents=True);(case/'test-bin').mkdir();(case/'test-bin/bash').symlink_to(a.bash.resolve())
write(case/'scripts/run-wukongim-three-node-chat-lifecycle-local-baseline.sh',(src/files[1]).read_text())
write(case/'scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh','''#!/usr/bin/env bash
set -euo pipefail
trap 'printf "term\\n" >>"$FAKE_SIGNAL_LOG"; sleep 0.25; printf "joined\\n" >>"$FAKE_SIGNAL_LOG"; exit 143' TERM
printf '%s\\n' "$$" >"$FAKE_CHILD_PID_FILE"
: >"$FAKE_READY_FILE"
while :; do sleep 0.05; done
''')
write(case/'test-bin/ps','#!/usr/bin/env bash\nexit 0\n')
write(case/'test-bin/df',"#!/usr/bin/env bash\nprintf 'Filesystem 1024-blocks Used Available Capacity Mounted on\\nfake 100000 10000 90000 10%% /\\n'\n")
env=os.environ.copy();env.update({'PATH':str(case/'test-bin')+':'+env['PATH'],'WK_BENCH_API_TOKEN':'fixture-api-token','WK_BENCH_WORKER_TOKEN':'fixture-worker-token','FAKE_READY_FILE':str(case/'child.ready'),'FAKE_CHILD_PID_FILE':str(case/'child.pid'),'FAKE_SIGNAL_LOG':str(case/'child-signals.log')})
# A wrapper exec keeps signal delivery on the exact baseline PID while adding its CLI.
script=out/'baseline-original.sh';write(script,'exec '+str(a.bash)+' -x '+str(case/'scripts/run-wukongim-three-node-chat-lifecycle-local-baseline.sh')+' --run-dir '+str(case/'baseline-run')+' --base-port 25000\n')
d=run('baseline-original',script,env,case/'child.ready');d['signals']=(case/'child-signals.log').read_text() if (case/'child-signals.log').exists() else None
original=(src/files[1]).read_text();needle='forward_operator_signal_to_active_step\n    return\n';assert needle in original
write(case/'scripts/run-wukongim-three-node-chat-lifecycle-local-baseline.sh',original.replace(needle,needle.replace('return\n','return 0\n'),1))
for f in ['child.ready','child.pid','child-signals.log']:(case/f).unlink(missing_ok=True)
text=script.read_text().replace(str(case/'baseline-run'),str(case/'baseline-run-control'));write(script,text)
d=run('baseline-return-zero',script,env,case/'child.ready');d['signals']=(case/'child-signals.log').read_text() if (case/'child-signals.log').exists() else None
receipt={'source_sha':source_sha,'files_sha256':freeze,'bash':str(a.bash),'bash_version':subprocess.check_output([str(a.bash),'--version'],text=True).splitlines()[0],'platform':subprocess.check_output(['uname','-sm'],text=True).strip(),'fixtures_only':True,'cloud_calls':False,'hypotheses':['Empty bridge return under failing EXIT trap aborts subsequent close on Bash5; explicit return0 fixes only cleanup while preserving exit17.','Baseline signals after ready installed trap; actual source/fixture trace distinguishes parent early exit from missing readiness.','Interrupted wait/second wait status255 differs across Bash versions and can reject a joined observer.'],'results':results}
(out/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps(receipt,indent=2))
