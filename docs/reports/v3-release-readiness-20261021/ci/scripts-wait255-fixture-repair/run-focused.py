import os,subprocess,json,time,pathlib,sys
p=pathlib.Path(__file__).resolve().parent
w='/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-shakeout-grant-repair'
shell=sys.argv[1];label=sys.argv[2];count=sys.argv[3] if len(sys.argv)>3 else '1'
env=os.environ.copy();env.update(GOWORK='off',GOENV='off',GOTOOLCHAIN='go1.25.11',GOFLAGS='-p=2',LC_ALL='C',GOCACHE='/tmp/wukongim-v3-readiness-20261021/local-shakeout-ed705-diagnosis/go-cache');env['PATH']='/tmp/wukongim-v3-readiness-20261021/scripts-signal-analysis-diagnosis/'+shell+'-bin:'+env['PATH']
args=['go','test','-json','-tags=integration','./scripts','-run','^(TestSignalJoinFixtureOperatorRejectsFailuresAndCleansChildren|TestChatLifecycleShakeoutJoinSurvivesFirstSignal|TestSingleNodeTerminalObserverJoinSurvivesParentSignal)$','-count='+count,'-timeout=90s','-parallel=2']
(p/(label+'-command.json')).write_text(json.dumps({'args':args,'cwd':w,'env':{k:env[k] for k in ['GOWORK','GOENV','GOTOOLCHAIN','GOFLAGS','LC_ALL','GOCACHE','PATH']}},indent=2)+'\n');start=time.monotonic()
with (p/(label+'.jsonl')).open('x') as f:r=subprocess.run(args,cwd=w,env=env,stdout=f,stderr=subprocess.STDOUT)
(p/(label+'-result.json')).write_text(json.dumps({'exit_code':r.returncode,'duration':time.monotonic()-start})+'\n');print(label,r.returncode,flush=True)
