import datetime,hashlib,json,os,pathlib,signal,subprocess,sys,tempfile,time,unittest
WATCHER=pathlib.Path(__file__).with_name('watch-three-node-first-grant.proposed.py')
h=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
REV='1'*40
class ThreeNode(unittest.TestCase):
 def prepare(self,d,mode='normal',lag=.01,bad_slot=False,bad_source=False):
  root=pathlib.Path(d);run=root/'run';run.mkdir();(run/'config').mkdir();(run/'evidence').mkdir();(run/'report').mkdir();source=root/'source';source.mkdir();binary=root/'binary';binary.write_bytes(b'synthetic-not-a-product');launcher=run/'launcher.sh';launcher.write_text('#!/bin/sh\n# synthetic fixture mapping only; never executed\n');fakebin=root/'fakebin';fakebin.mkdir();git=fakebin/'git';git.write_text('#!/bin/sh\nprintf "%s\\n" '+REV+'\n');git.chmod(0o700)
  for n in [1,2,3]:(run/f'config/node{n}.toml').write_text(f'[node]\nid = {n}\n')
  slots={'schema':'wukongim/local-original-slot-convergence/v1','hash_slots':256,'logical_slots':12,'voters':[1,2,3],'stable_seconds':2.1,'inventories':[{'node':n,'voter':{'items':[{'node_log':{'node_id':(1 if bad_slot and n==3 else n)}} for i in range(12)]}} for n in [1,2,3]]};(run/'evidence/slot-convergence.json').write_text(json.dumps(slots))
  now=datetime.datetime.now(datetime.timezone.utc);start=now-datetime.timedelta(seconds=lag);receipt={'schema':'wukongim.chat_lifecycle.run_start/v1','stage':'shakeout','started_at':start.isoformat(),'expected_end_at':(start+datetime.timedelta(seconds=259200)).isoformat(),'run_hash':'sha256:'+'a'*64,'assignment_hash':'sha256:'+'b'*64,'generation':1};(run/'report/run-start.json').write_text(json.dumps(receipt))
  helper=root/'fake-helper.sh';helper.write_text('#!/bin/bash\nexec python3 "$FAKE_HELPER_SCRIPT" "$@"\n');helper.chmod(0o700);script=root/'fake-helper.py';script.write_text(r"""import json,os,pathlib,sys,time
args=dict(zip(sys.argv[1::2],sys.argv[2::2]));node=int(args.get('--node-id','2'));root=pathlib.Path(os.environ['FAKE_ROOT']);out=pathlib.Path(args['--out-dir']);out.mkdir(parents=True,exist_ok=True)
(root/f'node-{node}.pid').write_text(str(os.getpid()));(root/f'node-{node}-args.json').write_text(json.dumps(args))
if os.environ['FAKE_MODE']=='block':time.sleep(30)
(out/'metadata.json').write_text(json.dumps({'capture':{'valid':True,'reason':'ok'},'nodes':[{'node':f'node-{node}'}]}));(out/'requests').mkdir()
for kind in ['cpu','goroutine']:(out/f'requests/node-{node}-{kind}.json').write_text(json.dumps({'node_id':node,'kind':kind,'curl_exit_code':0,'http_status':'200'}))
""")
  env=dict(os.environ);env.update(PATH=str(fakebin)+os.pathsep+env['PATH'],FAKE_HELPER_SCRIPT=str(script),FAKE_ROOT=str(root),FAKE_MODE=mode,WK_BENCH_API_TOKEN='synthetic-fixture-token')
  args=[str(WATCHER),'--run-dir',str(run),'--out-dir',str(run/'profiles-three-node'),'--source-root',str(source),'--source-revision',('2'*40 if bad_source else REV),'--binary',str(binary),'--binary-sha256',h(binary),'--helper',str(helper),'--helper-sha256',h(helper),'--launcher-sha256',h(launcher),'--base-port','25000','--run-hash',receipt['run_hash'],'--assignment-hash',receipt['assignment_hash'],'--generation','1','--deadline-mono',str(time.monotonic()+30)]
  command=[sys.executable,'-c','import os,sys;os.execv(sys.executable,[sys.executable]+sys.argv[1:]+["--owner-pgid",str(os.getpgrp())])',*args]
  return root,run,env,command
 def run_case(self,**kwargs):
  with tempfile.TemporaryDirectory() as d:
   root,run,env,cmd=self.prepare(d,**kwargs);p=subprocess.Popen(cmd,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
   try:stdout,stderr=p.communicate(timeout=4);self.assertEqual(p.returncode,0,stderr.decode());row=json.loads((run/'profile-driver-status.json').read_text());return row,len(list(root.glob('node-?.pid')))
   finally:
    if p.poll() is None:os.killpg(p.pid,signal.SIGKILL);p.wait(timeout=1)
 def test_one_receipt_launches_three_correct_nodes(self):
  row,count=self.run_case();self.assertEqual(count,3);self.assertEqual(row['status'],'complete');self.assertEqual([n['node_id'] for n in row['nodes']],[1,2,3]);self.assertTrue(all(n['capture_joined'] and n['helper_exit_status']==0 for n in row['nodes']))
 def test_stale_receipt_and_wrong_source_launch_none(self):
  for opts in [{'lag':3},{'bad_source':True}]:
   with self.subTest(opts=opts):row,count=self.run_case(**opts);self.assertEqual(count,0);self.assertEqual(row['status'],'skipped')
 def test_wrong_observed_slot_node_launches_none(self):
  row,count=self.run_case(bad_slot=True);self.assertEqual(count,0);self.assertEqual(row['status'],'skipped')
 def test_terminal_stop_cancels_and_joins_three_helpers(self):
  with tempfile.TemporaryDirectory() as d:
   root,run,env,cmd=self.prepare(d,mode='block');p=subprocess.Popen(cmd,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
   try:
    deadline=time.monotonic()+2
    while len(list(root.glob('node-?.pid')))<3 and p.poll() is None and time.monotonic()<deadline:time.sleep(.01)
    self.assertEqual(len(list(root.glob('node-?.pid'))),3);(run/'evidence/profile-stop').write_text('fixture-stop\n');stdout,stderr=p.communicate(timeout=4);row=json.loads((run/'profile-driver-status.json').read_text());self.assertEqual(row['status'],'partial');self.assertTrue(all(n['capture_joined'] for n in row['nodes']))
    for f in root.glob('node-?.pid'):
     with self.assertRaises(ProcessLookupError):os.kill(int(f.read_text()),0)
   finally:
    if p.poll() is None:os.killpg(p.pid,signal.SIGKILL);p.wait(timeout=1)
if __name__=='__main__':unittest.main()
