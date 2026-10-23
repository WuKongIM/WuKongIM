import json,os,pathlib,signal,subprocess,time,unittest
from watcher_coordination_test import ThreeNode

class OnceStatus(ThreeNode):
 def test_existing_claim_status_survives_all_guard_failures(self):
  cases=['wrong_source','wrong_launcher','wrong_slot','unsafe_owner','unsafe_output']
  for case in cases:
   with self.subTest(case=case),self._temporary() as d:
    root,run,env,cmd=self.prepare(d,bad_source=case=='wrong_source',bad_slot=case=='wrong_slot')
    (run/'profile-driver.claim').mkdir(); status=run/'profile-driver-status.json';status.write_bytes(b'{"original_first_status":true}\n');before=(status.read_bytes(),status.stat().st_mtime_ns)
    if case=='wrong_launcher':cmd[cmd.index('--launcher-sha256')+1]='0'*64
    if case=='unsafe_output':cmd[cmd.index('--out-dir')+1]=str(root/'unsafe-profiles')
    if case=='unsafe_owner':cmd[2]=cmd[2].replace('str(os.getpgrp())','str(os.getpgrp()+99999)')
    p=subprocess.Popen(cmd,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
    try:
     stdout,stderr=p.communicate(timeout=3);self.assertEqual(p.returncode,0,stderr.decode());self.assertEqual((status.read_bytes(),status.stat().st_mtime_ns),before);self.assertEqual(len(list(root.glob('node-?.pid'))),0);self.assertNotIn(b'synthetic-fixture-token',stdout+stderr)
    finally:
     if p.poll() is None:os.killpg(p.pid,signal.SIGKILL);p.wait(timeout=1)
 def _temporary(self):
  import tempfile
  return tempfile.TemporaryDirectory()
 def test_concurrent_start_has_one_claim_owner_and_three_helpers(self):
  with self._temporary() as d:
   root,run,env,cmd=self.prepare(d,mode='block');script=root/'fake-helper.py';script.write_text(script.read_text().replace("(root/f'node-{node}.pid').write_text(str(os.getpid()));", "with (root/'helper-launches.jsonl').open('a') as events:events.write(json.dumps({'node':node,'pid':os.getpid()})+'\\n')\n(root/f'node-{node}.pid').write_text(str(os.getpid()));"))
   # Both watcher processes share the same owned group as in the real launcher.
   args=cmd[3:];runner=root/'race-runner.py';runner.write_text("import json,os,pathlib,signal,subprocess,sys,time\nargs=json.loads(sys.argv[1]);env=os.environ;command=[sys.executable]+args+['--owner-pgid',str(os.getpgrp())]\nps=[subprocess.Popen(command,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE) for _ in range(2)]\nroot=pathlib.Path(os.environ['FAKE_ROOT']);end=time.monotonic()+2\nwhile len(list(root.glob('node-?.pid')))<3 and time.monotonic()<end:time.sleep(.01)\n(root/'run/evidence/profile-stop').write_text('fixture-stop\\n')\nrows=[]\nfor p in ps:\n out,err=p.communicate(timeout=4);rows.append({'exit':p.returncode,'stdout':out.decode(),'stderr':err.decode()})\n(root/'race-results.json').write_text(json.dumps(rows))\n")
   p=subprocess.Popen([os.sys.executable,str(runner),json.dumps(args)],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
   try:
    stdout,stderr=p.communicate(timeout=7);self.assertEqual(p.returncode,0,stderr.decode());rows=json.loads((root/'race-results.json').read_text());self.assertTrue(all(r['exit']==0 for r in rows));self.assertEqual(sum('already_claimed' in r['stdout'] for r in rows),1);launches=(root/'helper-launches.jsonl').read_text().splitlines();self.assertEqual(len(launches),3);status=json.loads((run/'profile-driver-status.json').read_text());self.assertEqual(len(status['nodes']),3);self.assertTrue(all(n['capture_joined'] for n in status['nodes']));self.assertNotIn('synthetic-fixture-token',json.dumps(rows))
    for f in root.glob('node-?.pid'):
     with self.assertRaises(ProcessLookupError):os.kill(int(f.read_text()),0)
   finally:
    if p.poll() is None:os.killpg(p.pid,signal.SIGKILL);p.wait(timeout=1)
if __name__=='__main__':unittest.main()
