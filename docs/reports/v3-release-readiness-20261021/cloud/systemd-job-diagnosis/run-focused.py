#!/usr/bin/env python3
import datetime,hashlib,json,os,pathlib,subprocess,sys,time
root=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job')
out=pathlib.Path('/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis')
phase=sys.argv[1]
assert phase in ['red','green']
files=['scripts/chat-lifecycle/classify-stage-service-state.sh','scripts/chat-lifecycle/pre-clock-diagnostic.py','scripts/chat_lifecycle_stage_state_test.go','scripts/chat_lifecycle_preclock_capture_integration_test.go']
def hashes(): return {p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in files}
for label,shell in [('bash3','/bin/bash'),('bash5','/tmp/wukongim-v3-readiness-20261021/scripts-signal-analysis-diagnosis/bash-5.2.21/bash')]:
    bindir=out/label/'bin';bindir.mkdir(parents=True,exist_ok=True)
    if not (bindir/'bash').exists(): (bindir/'bash').symlink_to(shell)
    env=dict(os.environ,PATH=str(bindir)+os.pathsep+os.environ['PATH'],LC_ALL='C',LANG='C',GOTOOLCHAIN='go1.25.11',GOWORK='off',GOFLAGS='-p=2',GOMAXPROCS='2',GOCACHE=str(out/'gocache'))
    for tier,args in [('default',['-run','^TestChatLifecycleStageServiceStateContract$']),('integration',['-tags=integration','-run','^TestChatLifecyclePreClockActualReadinessBranchesFreezeBeforeKill$'])]:
        prefix=out/(phase+'-'+label+'-'+tier)
        assert not prefix.with_suffix('.jsonl').exists(),prefix
        cmd=['go','test','./scripts','-count=1','-json','-timeout=90s',*args]
        before=hashes();start=time.monotonic();at=datetime.datetime.now(datetime.timezone.utc).isoformat()
        with prefix.with_suffix('.jsonl').open('wb') as stdout,prefix.with_suffix('.stderr').open('wb') as stderr:
            run=subprocess.run(cmd,cwd=root,env=env,stdout=stdout,stderr=stderr,timeout=240)
        events=[json.loads(line) for line in prefix.with_suffix('.jsonl').read_text().splitlines()]
        rec={'started_at':at,'elapsed_seconds':time.monotonic()-start,'command':cmd,'cwd':str(root),'env':{k:env[k] for k in ['LC_ALL','LANG','GOTOOLCHAIN','GOWORK','GOFLAGS','GOMAXPROCS','GOCACHE']},'shell':shell,'shell_sha256':hashlib.sha256(pathlib.Path(shell).read_bytes()).hexdigest(),'shell_version':subprocess.check_output([shell,'--version'],env=env,text=True).splitlines()[0],'go_version':subprocess.check_output(['go','version'],env=env,text=True).strip(),'exit_code':run.returncode,'before':before,'after':hashes(),'failed_tests':[e['Test'] for e in events if e.get('Action')=='fail' and 'Test' in e],'passed_tests':[e['Test'] for e in events if e.get('Action')=='pass' and 'Test' in e],'skipped_tests':[e['Test'] for e in events if e.get('Action')=='skip' and 'Test' in e]}
        for suffix in ['.jsonl','.stderr']:
            b=prefix.with_suffix(suffix).read_bytes();rec[suffix]={'size':len(b),'sha256':hashlib.sha256(b).hexdigest()}
        prefix.with_suffix('.receipt.json').write_text(json.dumps(rec,indent=2)+'\n')
        print(phase,label,tier,'exit',run.returncode,'pass',len(rec['passed_tests']),'fail',len(rec['failed_tests']),'skip',len(rec['skipped_tests']),flush=True)
