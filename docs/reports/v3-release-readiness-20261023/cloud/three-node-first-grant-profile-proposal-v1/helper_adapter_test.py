import datetime,json,os,pathlib,signal,subprocess,sys,tempfile,time,unittest
HELPER=pathlib.Path(__file__).with_name('capture-three-node-first-grant-profiles.sh')
TOKEN='synthetic-token-not-a-real-secret'
class HelperIdentity(unittest.TestCase):
 def setup_case(self,d,case='normal'):
  root=pathlib.Path(d);bindir=root/'fake-bin';bindir.mkdir();log=root/'calls.jsonl'
  curl=bindir/'curl';curl.write_text('#!'+sys.executable+'\n'+r"""import json,os,pathlib,signal,sys,time
args=sys.argv[1:];out=pathlib.Path(args[args.index('--output')+1]);url=args[-1]
assert 'WK_BENCH_API_TOKEN' not in os.environ
with open(os.environ['FAKE_CALLS'],'a') as f:f.write(json.dumps({'pid':os.getpid(),'url':url,'destination':str(out),'has_auth_env':False})+'\n')
if os.environ['FAKE_CASE']=='block':time.sleep(30)
if os.environ['FAKE_CASE']=='http_error':print('503',end='');sys.exit(22)
out.write_bytes(b'synthetic-profile-body');print('200',end='')
""");curl.chmod(0o700)
  sleep=bindir/'sleep';sleep.write_text('#!/bin/sh\nexit 0\n');sleep.chmod(0o700)
  phase=root/'phase';phase.write_text('measurement\n');out=root/'out';env=dict(os.environ);env.update(PATH=str(bindir)+os.pathsep+env['PATH'],WK_BENCH_API_TOKEN=TOKEN,FAKE_CALLS=str(log),FAKE_CASE=case)
  return root,phase,out,env,log
 def command(self,phase,out,node=None):
  cmd=['bash',str(HELPER),'--out-dir',str(out),'--phase-state-file',str(phase),'--trigger-kind','first_grant_receipt','--trigger-observed-phase','measurement','--previous-utc','2026-10-23T00:00:00Z','--current-utc','2026-10-23T00:00:00.1Z','--node','http://127.0.0.1:'+str(25000+(int(node) if node in ['1','2','3'] else 2)),'--cpu-seconds','10']
  if node is not None:cmd+=['--node-id',node]
  return cmd
 def check_case(self,node=None,case='normal'):
  with tempfile.TemporaryDirectory() as d:
   root,phase,out,env,log=self.setup_case(d,case);r=subprocess.run(self.command(phase,out,node),env=env,capture_output=True,timeout=3);self.assertEqual(r.returncode,0,r.stderr.decode());self.assertNotIn(TOKEN.encode(),r.stdout+r.stderr)
   n=int(node or '2');m=json.loads((out/'metadata.json').read_text());self.assertEqual(m['nodes'][0]['node'],'node-'+str(n));self.assertEqual(m['capture']['valid'],case=='normal')
   calls=[json.loads(x) for x in log.read_text().splitlines()];self.assertEqual(len(calls),2);self.assertEqual({x['url'] for x in calls},{f'http://127.0.0.1:{25000+n}/debug/pprof/profile?seconds=10',f'http://127.0.0.1:{25000+n}/debug/pprof/goroutine?debug=0'})
   for kind,cap in [('cpu',8388608),('goroutine',4194304)]:
    req=json.loads((out/f'requests/node-{n}-{kind}.json').read_text());self.assertEqual(req['node_id'],n);self.assertEqual(req['max_bytes'],cap)
    self.assertEqual(req['error_class'],'none' if case=='normal' else 'http_error')
    self.assertEqual((out/f'profiles/node-{n}-{kind}.pb.gz').exists(),case=='normal')
 def test_explicit_three_node_identities(self):
  for n in ['1','2','3']:
   with self.subTest(node=n):self.check_case(n)
 def test_default_preserves_node2(self):self.check_case()
 def test_invalid_ids_fail_before_artifact_io(self):
  for n in ['0','4','-1','3.0','x','']:
   with self.subTest(node=n),tempfile.TemporaryDirectory() as d:
    root,phase,out,env,log=self.setup_case(d);r=subprocess.run(self.command(phase,out,n),env=env,capture_output=True,timeout=2);self.assertEqual(r.returncode,64);self.assertFalse(out.exists());self.assertFalse(log.exists());self.assertNotIn(TOKEN.encode(),r.stdout+r.stderr)
 def test_node3_partial_retains_producer_identity(self):self.check_case('3','http_error')
 def test_signal_cancel_joins_fakecurl_and_preserves_node3(self):
  with tempfile.TemporaryDirectory() as d:
   root,phase,out,env,log=self.setup_case(d,'block');p=subprocess.Popen(self.command(phase,out,'3'),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE);deadline=time.monotonic()+2
   while time.monotonic()<deadline:
    if log.exists() and len(log.read_text().splitlines())==2:break
    if p.poll() is not None:break
    time.sleep(.01)
   self.assertTrue(log.exists());p.send_signal(signal.SIGTERM);stdout,stderr=p.communicate(timeout=2);self.assertEqual(p.returncode,0);self.assertNotIn(TOKEN.encode(),stdout+stderr)
   m=json.loads((out/'metadata.json').read_text());self.assertEqual(m['nodes'][0]['node'],'node-3');self.assertFalse(m['capture']['valid']);self.assertEqual(m['capture']['reason'],'interrupted')
   for row in map(json.loads,log.read_text().splitlines()):
    with self.assertRaises(ProcessLookupError):os.kill(row['pid'],0)
if __name__=='__main__':unittest.main()
