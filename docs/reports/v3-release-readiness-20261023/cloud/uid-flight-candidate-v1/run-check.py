import datetime,json,os,pathlib,signal,subprocess,sys,time
out=pathlib.Path(__file__).parent
root=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-uid-flight-candidate')
label=sys.argv[1];argv=sys.argv[2:]
extra={'GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOWORK':'off','CGO_ENABLED':'0','GOMAXPROCS':'1'}
if '-race' in argv:extra['CGO_ENABLED']='1'
extra['PATH']='/Users/tt/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.11.darwin-arm64/bin:'+os.environ.get('PATH','')
start=datetime.datetime.now(datetime.timezone.utc).isoformat();tick=time.monotonic()
(out/(label+'-invocation.json')).write_text(json.dumps({'cwd':str(root),'argv':argv,'environment_overrides':extra,'started_utc':start,'outer_timeout_seconds':120},indent=2)+'\n')
p=subprocess.Popen(argv,cwd=root,env=dict(os.environ,**extra),stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
expired=False
try:stdout,stderr=p.communicate(timeout=120)
except subprocess.TimeoutExpired:
 expired=True;os.killpg(p.pid,signal.SIGTERM)
 try:stdout,stderr=p.communicate(timeout=2)
 except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL);stdout,stderr=p.communicate()
(out/(label+'-stdout.txt')).write_bytes(stdout);(out/(label+'-stderr.txt')).write_bytes(stderr)
receipt={'exit_code':p.returncode,'outer_timeout':expired,'child_joined':p.poll() is not None,'process_group':p.pid,'elapsed_seconds':time.monotonic()-tick,'started_utc':start,'finished_utc':datetime.datetime.now(datetime.timezone.utc).isoformat()}
(out/(label+'-receipt.json')).write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt));print(stdout.decode('utf-8','replace'));print(stderr.decode('utf-8','replace'))
