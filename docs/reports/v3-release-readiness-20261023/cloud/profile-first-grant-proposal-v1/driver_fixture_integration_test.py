import datetime,hashlib,http.server,json,os,pathlib,secrets,signal,socket,subprocess,sys,tempfile,threading,time,unittest,urllib.parse
BASE=pathlib.Path(__file__).resolve().parent
DRIVER=BASE/'watch-first-grant.py'
HELPER=BASE/'capture-first-grant-profiles.sh'
RESULTS=[]
class Fixture:
 def __init__(self,mode='normal',receipt='valid',wrong_source=False,remaining=35):
  self.root=pathlib.Path(tempfile.mkdtemp(prefix='wk-profile-fake-'));self.root.chmod(0o700);self.events=[];self.active=0;self.lock=threading.Lock();self.mode=mode;self.token=secrets.token_urlsafe(24);self.remaining=remaining
  src=self.root/'source';src.mkdir();subprocess.run(['git','init','-q',str(src)],check=True)
  subprocess.run(['git','-C',str(src),'-c','user.name=ProfileFixture','-c','user.email=profile-fixture@example.invalid','commit','-q','--allow-empty','-m','fake-only'],check=True)
  rev=subprocess.check_output(['git','-C',str(src),'rev-parse','HEAD'],text=True).strip();self.binary=self.root/'fake-binary';self.binary.write_bytes(b'fixture-only-not-a-product')
  self.run=self.root/'run';(self.run/'report').mkdir(parents=True);(self.run/'evidence').mkdir();self.output=self.run/'profile-evidence'
  now=datetime.datetime.now(datetime.timezone.utc);began=now if receipt!='stale' else now-datetime.timedelta(seconds=3)
  self.receipt={'schema':'wukongim.chat_lifecycle.run_start/v1','stage':'shakeout','started_at':began.isoformat(),'expected_end_at':(began+datetime.timedelta(days=3)).isoformat(),'run_hash':'sha256:'+'a'*64,'assignment_hash':'sha256:'+'b'*64,'generation':1}
  if receipt=='wrong_identity':self.receipt['assignment_hash']='sha256:'+'c'*64
  data=json.dumps(self.receipt).encode();path=self.run/'report/run-start.json'
  if receipt=='oversized':data+=b' '*5000
  if receipt=='symlink':
   (self.root/'receipt.json').write_bytes(data);path.symlink_to(self.root/'receipt.json')
  elif receipt!='missing':path.write_bytes(data)
  f=self
  class Handler(http.server.BaseHTTPRequestHandler):
   def log_message(self,*a):pass
   def do_GET(self):
    at=time.monotonic();kind='cpu' if self.path=='/debug/pprof/profile?seconds=10' else 'goroutine' if self.path=='/debug/pprof/goroutine?debug=2' else 'UNEXPECTED'
    with f.lock:f.events.append({'kind':kind,'at':at,'path':self.path,'auth_ok':self.headers.get('Authorization')=='Bearer '+f.token});f.active+=1
    try:
     self.send_response(200);self.end_headers()
     if kind=='cpu' and f.mode not in ['oversize_cpu','oversize_gor']:
      until=time.monotonic()+6
      while time.monotonic()<until:
       import select
       if select.select([self.connection],[],[],.02)[0]:
        if self.connection.recv(1,socket.MSG_PEEK)==b'':return
     size=(8*1024*1024+65536 if kind=='cpu' and f.mode=='oversize_cpu' else 4*1024*1024+65536 if kind=='goroutine' and f.mode=='oversize_gor' else 64)
     block=b'x'*16384
     while size:
      chunk=block[:min(size,len(block))];self.wfile.write(chunk);self.wfile.flush();size-=len(chunk)
    except (BrokenPipeError,ConnectionResetError,OSError):pass
    finally:
     with f.lock:f.active-=1
  self.server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler);self.server.daemon_threads=True;threading.Thread(target=self.server.serve_forever,daemon=True).start()
  env=dict(os.environ);env['WK_BENCH_API_TOKEN']=self.token
  self.args=[sys.executable,str(DRIVER),'--run-dir',str(self.run),'--out-dir',str(self.output),'--source-root',str(src),'--source-revision','0'*40 if wrong_source else rev,'--binary',str(self.binary),'--binary-sha256',hashlib.sha256(self.binary.read_bytes()).hexdigest(),'--helper',str(HELPER),'--helper-sha256',hashlib.sha256(HELPER.read_bytes()).hexdigest() if HELPER.exists() else '0'*64,'--base-port',str(self.server.server_port-2),'--run-hash','sha256:'+'a'*64,'--assignment-hash','sha256:'+'b'*64,'--generation','1','--deadline-mono',str(time.monotonic()+remaining)]
  self.process=subprocess.Popen(['bash','-c','exec "$@" --owner-pgid "$$"','profile-fixture',*self.args],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
 def wait(self,seconds=18):
  out,err=self.process.communicate(timeout=seconds)
  assert self.process.returncode==0,(self.process.returncode,err.decode()[:300])
  receipt=json.loads((self.run/'profile-driver-status.json').read_text())
  RESULTS.append({'fixture_only':True,'case':self.mode,'receipt':str(self.run/'profile-driver-status.json'),'driver_status':receipt['status'],'reason':receipt['reason'],'requests':self.events,'root':str(self.root)})
  return receipt
 def close(self):
  if self.process.poll() is None:os.killpg(self.process.pid,signal.SIGKILL);self.process.wait()
  self.server.shutdown();self.server.server_close()
 def assert_clean(self,t):
  until=time.monotonic()+1
  while self.active and time.monotonic()<until:time.sleep(.02)
  t.assertEqual(self.active,0)
  raw=subprocess.check_output(['ps','-axo','pid=,pgid=,comm='],text=True)
  t.assertFalse(any(len(s:=line.split(None,2))>=2 and int(s[1])==self.process.pid for line in raw.splitlines()))
  for f in self.run.rglob('*'):
   if f.is_file():t.assertNotIn(self.token.encode(),f.read_bytes())
class Contract(unittest.TestCase):
 def fixture(self,*a,**k):
  f=Fixture(*a,**k);self.addCleanup(f.close);return f
 def test_01_matching_fresh_receipt_schedules_only_node2_cpu10_and_gor_at5(self):
  f=self.fixture();r=f.wait();self.assertEqual(r['status'],'complete');self.assertIn('helper_launch_parent_observed_at',r);self.assertNotIn('cpu_submission_parent_observed_at',r);self.assertEqual([e['kind'] for e in f.events],['cpu','goroutine']);self.assertTrue(all(e['auth_ok'] for e in f.events));self.assertGreaterEqual(f.events[1]['at']-f.events[0]['at'],4.8);self.assertLess(f.events[1]['at']-f.events[0]['at'],5.5);self.assertTrue((f.output/'profiles/node-2-cpu.pb.gz').exists());self.assertTrue((f.output/'profiles/node-2-goroutine.txt').exists());self.assertFalse(any('heap' in e['path'] for e in f.events));f.assert_clean(self)
 def test_02_wrong_source_and_receipt_or_insufficient_budget_issue_no_requests(self):
  for kw in [{'wrong_source':True},{'receipt':'wrong_identity'},{'receipt':'stale'},{'receipt':'oversized'},{'receipt':'symlink'},{'remaining':5}]:
   with self.subTest(kw=kw):
    f=self.fixture(**kw);r=f.wait(4);self.assertNotEqual(r['status'],'complete');self.assertEqual(f.events,[]);f.assert_clean(self);f.close()
 def test_03_terminal_before_timer_cancels_and_joins_without_goroutine(self):
  f=self.fixture();time.sleep(1);(f.run/'evidence/coordinator-exit-status').write_text('8\n');r=f.wait(5);self.assertEqual(r['status'],'partial');self.assertEqual([e['kind'] for e in f.events],['cpu']);f.assert_clean(self)
 def test_04_signal_during_cpu_timer_and_after_gor_joins_every_child(self):
  for delay,sig in [(1,signal.SIGTERM),(1,signal.SIGHUP),(1,signal.SIGINT),(5.2,signal.SIGTERM)]:
   with self.subTest(delay=delay,signal=int(sig)):
    f=self.fixture();time.sleep(delay);os.kill(f.process.pid,sig);r=f.wait(5);self.assertEqual(r['status'],'partial');self.assertLessEqual(len(f.events),2);f.assert_clean(self);f.close()
 def test_07_repeated_driver_does_not_repeat_capture_or_overwrite_owner(self):
  f=self.fixture();time.sleep(.3)
  env=dict(os.environ);env['WK_BENCH_API_TOKEN']=f.token
  repeated=subprocess.run(['bash','-c','exec "$@" --owner-pgid "$$"','profile-fixture',*f.args],env=env,start_new_session=True,capture_output=True,timeout=3)
  self.assertEqual(repeated.returncode,0);self.assertIn(b'already_claimed',repeated.stdout)
  r=f.wait();self.assertEqual(r['status'],'complete');self.assertEqual([e['kind'] for e in f.events],['cpu','goroutine']);f.assert_clean(self)
 def test_06_waiting_receipt_cannot_consume_required_capture_budget(self):
  f=self.fixture(receipt='missing',remaining=21);time.sleep(2)
  now=datetime.datetime.now(datetime.timezone.utc);f.receipt['started_at']=now.isoformat();f.receipt['expected_end_at']=(now+datetime.timedelta(days=3)).isoformat();(f.run/'report/run-start.json').write_text(json.dumps(f.receipt))
  r=f.wait(4);self.assertEqual(r['status'],'skipped');self.assertEqual(f.events,[]);f.assert_clean(self)
 def test_05_headerless_oversized_cpu_and_gor_are_missing_never_promoted(self):
  for kind in ['cpu','gor']:
   with self.subTest(kind=kind):
    f=self.fixture(mode='oversize_'+kind);r=f.wait();self.assertEqual(r['status'],'partial');name='node-2-cpu.pb.gz' if kind=='cpu' else 'node-2-goroutine.txt';self.assertFalse((f.output/'profiles'/name).exists());self.assertFalse(list(f.output.rglob('*.next.*')));f.assert_clean(self);f.close()
if __name__=='__main__':
 try:
  program=unittest.main(exit=False,verbosity=2)
  test_status=0 if program.result.wasSuccessful() else 1
 finally:(BASE/'fake-test-results.json').write_text(json.dumps({'fixture_only':True,'cases':RESULTS,'driver_sha256':hashlib.sha256(DRIVER.read_bytes()).hexdigest() if DRIVER.exists() else None,'helper_sha256':hashlib.sha256(HELPER.read_bytes()).hexdigest() if HELPER.exists() else None},indent=2)+'\n')

 sys.exit(test_status)
