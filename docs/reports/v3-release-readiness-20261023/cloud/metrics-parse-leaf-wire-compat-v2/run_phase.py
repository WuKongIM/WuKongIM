from pathlib import Path
import subprocess,os,json,sys,time,datetime,hashlib,selectors,signal
e=Path(__file__).parent;wt=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-metrics-parse-leaf-diagnostics')
phase=sys.argv[1];args=json.loads(sys.argv[2]);outer=int(sys.argv[3]) if len(sys.argv)>3 else 300
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
pins=json.loads((e/'all-test-pins.json').read_text())
supplement=e/'supplement2-tests-first-pins.json'
if supplement.exists():pins['files'].update(json.loads(supplement.read_text())['files'])
assert all(sha(wt/p)==v['sha256'] for p,v in pins['files'].items())
claim=e/(phase+'.claim');claim.open('x').close()
env=os.environ.copy();changes={'GOWORK':'off','GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOMAXPROCS':'2','GOFLAGS':'-p=1'};env.update(changes)
started=datetime.datetime.now(datetime.timezone.utc).isoformat();start=time.monotonic()
proc=subprocess.Popen(args,cwd=wt,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
sel=selectors.DefaultSelector();sel.register(proc.stdout,selectors.EVENT_READ,'stdout');sel.register(proc.stderr,selectors.EVENT_READ,'stderr')
limits={'stdout':4<<20,'stderr':1<<20};data={'stdout':bytearray(),'stderr':bytearray()};observed={'stdout':0,'stderr':0};reason='exited';killed=False
while sel.get_map():
 remaining=outer-(time.monotonic()-start)
 if remaining<=0 and not killed:reason='timeout';os.killpg(proc.pid,signal.SIGKILL);killed=True
 for key,_ in sel.select(.1 if killed else min(.25,max(remaining,0))):
  chunk=os.read(key.fd,65536)
  if not chunk:sel.unregister(key.fileobj);continue
  name=key.data;observed[name]+=len(chunk);data[name].extend(chunk[:max(0,limits[name]-len(data[name]))])
  if observed[name]>limits[name] and not killed:reason='output_limit';os.killpg(proc.pid,signal.SIGKILL);killed=True
 if killed and time.monotonic()-start>outer+5:break
code=proc.wait(timeout=5);sel.close()
for name,body in data.items():(e/(phase+'.'+name+'.log')).write_bytes(body)
unchanged=all(sha(wt/p)==v['sha256'] for p,v in pins['files'].items());assert unchanged
receipt={'phase':phase,'args':args,'environment':changes,'started_at':started,'wall_seconds':time.monotonic()-start,'exit_code':code,'reason':reason,'pid_pgid':proc.pid,'observed_bytes':observed,'limits':limits,'outer_seconds':outer,'test_pins_sha256':sha(e/'all-test-pins.json'),'same_test_hashes':True,'supplemental_test_pins_sha256':sha(supplement) if supplement.exists() else None,'source_diff':subprocess.check_output(['git','diff','--stat'],cwd=wt,text=True)}
(e/(phase+'.json')).write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps({k:receipt[k] for k in ['phase','wall_seconds','exit_code','reason','observed_bytes','same_test_hashes']}))
if code:
 lines=(bytes(data['stderr']).decode(errors='replace')+'\n'+bytes(data['stdout']).decode(errors='replace')).splitlines()
 print('\n'.join(lines[:25]))
