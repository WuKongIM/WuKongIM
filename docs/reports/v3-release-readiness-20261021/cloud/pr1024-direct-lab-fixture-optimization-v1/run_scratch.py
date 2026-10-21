#!/usr/bin/env python3
import pathlib,subprocess,os,json,time,signal,sys,hashlib
D=pathlib.Path(__file__).resolve().parent
R=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-evidence')
kind=sys.argv[1];assert kind in ['baseline-first','variant-first']
P=D/kind;P.mkdir(exist_ok=False)
env=os.environ.copy();env.update({'GOWORK':'off','GOFLAGS':'-p=2','DIRECT_LAB_FIXTURE_SOURCE':str(R),'DIRECT_LAB_GO_TRACE_SOURCE':str(D/'go-trace.py'),'DIRECT_LAB_REAL_GO':'/usr/local/go/bin/go','DIRECT_LAB_DIAG_ARTIFACT':str(P/'artifact.json'),'DIRECT_LAB_INJECT_UNUSED_TOOLS':str(kind=='variant-first').lower()})
argv=['/usr/local/go/bin/go','test',str(D/'fixture_test.go'),'-count=1','-timeout=70s','-parallel=2','-json']
(P/'command.json').write_text(json.dumps({'argv':argv,'cwd':str(R),'scratch_timeout_s':90,'single_variable':{'DIRECT_LAB_INJECT_UNUSED_TOOLS':env['DIRECT_LAB_INJECT_UNUSED_TOOLS']},'source_sha256':hashlib.sha256((D/'fixture_test.go').read_bytes()).hexdigest()},indent=2)+'\n')
def members(group):
 out=subprocess.check_output(['ps','-eo','pid=,pgid='],text=True)
 return [int(x.split()[0]) for x in out.splitlines() if len(x.split())==2 and int(x.split()[1])==group]
start=time.monotonic();timedout=False
with (P/'stdout-stderr.jsonl').open('w') as log:
 p=subprocess.Popen(argv,cwd=R,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
 try:p.wait(timeout=90)
 except subprocess.TimeoutExpired:
  timedout=True;os.killpg(p.pid,signal.SIGTERM)
  try:p.wait(timeout=3)
  except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL);p.wait()
 initial=members(p.pid)
 if initial:
  os.killpg(p.pid,signal.SIGKILL)
 remaining=members(p.pid)
 result={'exit_code':p.returncode,'timed_out':timedout,'elapsed_s':time.monotonic()-start,'owned_pgid':p.pid,'cleanup_initial':initial,'cleanup_remaining':remaining,'cleanup_verified':not remaining}
(P/'result.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result))
