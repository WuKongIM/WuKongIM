import sys
sys.dont_write_bytecode=True
from pathlib import Path
import json,importlib.util,time,subprocess,shutil,os,statistics
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-evidence')
D=Path('/tmp/wukongim-v3-readiness-20261021/cloud/pr1024-fixture-profiling')
spec=importlib.util.spec_from_file_location('fixture',ROOT/'scripts/testdata/chat_lifecycle_terminal_evidence_test.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
bin=D/'bin';names=['ssh','timeout','python3','jq','wc','tr','install','rm','mktemp','date']
original_ssh=m.SSH
m.SSH='''#!/bin/sh
command=""
for arg do command="$arg"; done
case "$command" in
  *worker-evidence.json*|*diagnostic-status.json*|*api/v1/query_range*) exec python3 "$PROFILE_FALLBACK_SSH" "$@" ;;
  *api/v1/targets*) printf '%s\\n' '{"kind":"targets"}' >>"$PROFILE_CALLS"; printf '%s' '{"status":"success","data":{"activeTargets":[]}}' ;;
  *) printf '%s\\n' '{"kind":"other"}' >>"$PROFILE_CALLS"; printf 'fixture terminal capture\\n' ;;
esac
'''
original_run=subprocess.run
current={}
def traced_run(command,*args,**kwargs):
    env=dict(kwargs['env']);original_path=env['PATH'];env['PROFILE_PHASE_LOG']=str(current['trace'])
    fallback=Path(shutil.which('ssh',path=original_path)).with_name('python-ssh-fixture');fallback.write_text(original_ssh);fallback.chmod(0o700)
    env['PROFILE_FALLBACK_SSH']=str(fallback);env['PROFILE_CALLS']=json.loads(Path(env['WK_TEST_CONFIG']).read_text())['calls']
    for name in names:
        real=shutil.which(name,path=original_path);assert real
        env['PROFILE_REAL_'+name]=real
    env['PATH']=str(bin)+os.pathsep+original_path;kwargs['env']=env
    start=time.monotonic();result=original_run(command,*args,**kwargs);current['collector_seconds']=time.monotonic()-start
    return result
m.subprocess.run=traced_run
cases=[('normal-1',{}),('normal-2',{}),('large',{'metric_modes':dict.fromkeys(m.FAMILIES,'large')}),('missing-worker',{'document_modes':{'rehearsal/worker-evidence.json':'missing'}}),('invalid-anchor',{'report_mutation':lambda x:x['window'].pop('end')}),('formal-capacity',{'stage':'formal','capacity':True})]
results=[]
try:
 for name,args in cases:
  current={'trace':D/(name+'-fast-host-trace.jsonl')};start=time.monotonic();rc,manifest,calls,retained,files=m.CollectorTests().run_case(name,**args);wall=time.monotonic()-start
  trace=[json.loads(s) for s in current['trace'].read_text().splitlines()];groups={}
  for tool in names:
   events=[e for e in trace if e['tool']==tool];groups[tool]={'count':len(events),'inclusive_seconds':sum(e['duration'] for e in events)}
  result={'case':name,'fixture_seconds':wall,'collector_seconds':current['collector_seconds'],'collector_exit':rc,'complete':manifest['complete'],'request_count':len(calls),'tools':groups,'note':'inclusive durations overlap: timeout wraps ssh; python is inside ssh or main helper'}
  results.append(result);(D/'fast-host-results.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(result),flush=True)
finally:m.subprocess.run=original_run
