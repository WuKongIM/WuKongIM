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
    at=time.monotonic();kind='cpu' if self.path=='/debug/pprof/profile?seconds=10' else 'goroutine' if self.path=='/debug/pprof/goroutine?debug=0' else 'UNEXPECTED'
    with f.lock:f.events.append({'kind':kind,'at':at,'path':self.path,'auth_ok':self.headers.get('Authorization')=='Bearer '+f.token});f.active+=1
    try:
     response_code=503 if kind=='goroutine' and f.mode=='http503' else 302 if kind=='goroutine' and f.mode=='http302' else 200
     self.send_response(response_code);self.end_headers()
     if kind=='goroutine' and f.mode=='gor_timeout':
      until=time.monotonic()+6
      while time.monotonic()<until:
       import select
       if select.select([self.connection],[],[],.02)[0] and self.connection.recv(1,socket.MSG_PEEK)==b'':return
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
 def test_protobuf_and_finite_status_contract(self):
  cases=[('normal','complete',0,'200','none'),('http503','partial',22,'503','http_error'),('http302','partial',0,'302','http_status'),('gor_timeout','partial',28,'200','timeout'),('oversize_gor','partial',63,'200','max_filesize')]
  for mode,status,code,http,error in cases:
   with self.subTest(mode=mode):
    f=self.fixture(mode=mode);r=f.wait();self.assertEqual(r['status'],status)
    self.assertEqual([e['kind'] for e in f.events],['cpu','goroutine']);self.assertTrue(all(e['auth_ok'] for e in f.events))
    self.assertEqual(f.events[1]['path'],'/debug/pprof/goroutine?debug=0')
    evidence=json.loads((f.output/'requests/node-2-goroutine.json').read_text())
    self.assertEqual(evidence['curl_exit_code'],code);self.assertEqual(evidence['http_status'],http);self.assertEqual(evidence['error_class'],error)
    self.assertEqual((f.output/'profiles/node-2-goroutine.pb.gz').exists(),mode=='normal');self.assertFalse((f.output/'profiles/node-2-goroutine.txt').exists());self.assertFalse(list(f.output.rglob('*.next.*')))
    f.assert_clean(self);f.close()
 def test_cancel_still_joins_and_records_finite_status(self):
  f=self.fixture();time.sleep(1);os.kill(f.process.pid,signal.SIGTERM);r=f.wait(5)
  self.assertEqual(r['status'],'partial');self.assertEqual([e['kind'] for e in f.events],['cpu'])
  evidence=json.loads((f.output/'requests/node-2-cpu.json').read_text());self.assertIsInstance(evidence['curl_exit_code'],int);self.assertIn(evidence['error_class'],['interrupted','none'])
  f.assert_clean(self)
if __name__=='__main__':
 try:
  program=unittest.main(exit=False,verbosity=2)
  test_status=0 if program.result.wasSuccessful() else 1
 finally:(BASE/'fake-test-results.json').write_text(json.dumps({'fixture_only':True,'cases':RESULTS,'driver_sha256':hashlib.sha256(DRIVER.read_bytes()).hexdigest() if DRIVER.exists() else None,'helper_sha256':hashlib.sha256(HELPER.read_bytes()).hexdigest() if HELPER.exists() else None},indent=2)+'\n')

 sys.exit(test_status)
