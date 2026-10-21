#!/usr/bin/env python3
import pathlib,subprocess,os,json,time,signal,sys,hashlib
D=pathlib.Path(__file__).resolve().parent
R=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-evidence')
kind=sys.argv[1];commands={
'default-list-first':['go','test','./scripts','-list','^TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire$'],
'integration-list-first':['go','test','-tags=integration','./scripts','-list','^TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire$'],
'default-focused-first':['go','test','./scripts','-run','^TestChatLifecycleDirectLab','-count=1','-timeout=70s','-json'],
'integration-focused-first':['go','test','-tags=integration','./scripts','-run','^TestChatLifecycleDirectLab','-count=1','-timeout=70s','-parallel=2','-json'],
};argv=commands[kind];P=D/kind;P.mkdir(exist_ok=False)
env=os.environ.copy();env.update({'GOWORK':'off','GOFLAGS':'-p=2'})
(P/'command.json').write_text(json.dumps({'argv':argv,'cwd':str(R),'outer_local_watchdog_s':90,'environment':{'GOWORK':'off','GOFLAGS':'-p=2'},'source_files':[{'path':x,'sha256':hashlib.sha256((R/x).read_bytes()).hexdigest()} for x in ['scripts/chat_lifecycle_direct_lab_test.go','scripts/chat_lifecycle_direct_lab_start_integration_test.go','docs/development/PROJECT_KNOWLEDGE.md']]},indent=2)+'\n')
def members(group):
 return [int(x.split()[0]) for x in subprocess.check_output(['ps','-eo','pid=,pgid='],text=True).splitlines() if len(x.split())==2 and int(x.split()[1])==group]
start=time.monotonic();timedout=False
with (P/'stdout-stderr.log').open('w') as log:
 p=subprocess.Popen(argv,cwd=R,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
 try:p.wait(timeout=90)
 except subprocess.TimeoutExpired:
  timedout=True;os.killpg(p.pid,signal.SIGTERM)
  try:p.wait(timeout=3)
  except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL);p.wait()
 initial=members(p.pid)
 if initial:os.killpg(p.pid,signal.SIGKILL)
 remaining=members(p.pid)
result={'exit_code':p.returncode,'timed_out':timedout,'elapsed_s':time.monotonic()-start,'owned_pgid':p.pid,'cleanup_initial':initial,'cleanup_remaining':remaining,'cleanup_verified':not remaining}
(P/'result.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result))
