import json,os,pathlib,signal,subprocess,time,unittest
from watcher_coordination_test import ThreeNode

class CancelDescendants(ThreeNode):
 def _case(self,mode):
  import tempfile
  with tempfile.TemporaryDirectory() as d:
   root,run,env,cmd=self.prepare(d,mode='block');program=pathlib.Path(__file__).with_name('fake-orphan-helper.py');env.update(FAKE_HELPER_SCRIPT=str(program),ORPHAN_MODE=mode)
   p=subprocess.Popen(cmd,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True);children=[]
   try:
    end=time.monotonic()+2
    while len(list(root.glob('child-?.ready')))<3 and time.monotonic()<end:time.sleep(.01)
    self.assertEqual(len(list(root.glob('child-?.ready'))),3);children=[int(f.read_text()) for f in root.glob('child-?.pid')]
    if mode!='exit-before-cancel':(run/'evidence/profile-stop').write_text('synthetic-stop\n')
    started=time.monotonic();out,err=p.communicate(timeout=5);elapsed=time.monotonic()-started;self.assertEqual(p.returncode,0,err.decode());row=json.loads((run/'profile-driver-status.json').read_text());self.assertEqual(row['status'],'partial');self.assertTrue(all(n['capture_joined'] for n in row['nodes']));self.assertNotIn(b'synthetic-fixture-token',out+err)
    print(json.dumps({'fixture_mode':mode,'cancel_to_watcher_join_seconds':elapsed,'retained_descendants':len(row['cancel_retained_descendant_pids']),'remaining_observed_descendants':len(row['cancel_remaining_descendant_pids']),'descendant_cleanup_confirmed':row['cancel_descendant_cleanup_confirmed'],'direct_helpers_joined':all(n['capture_joined'] for n in row['nodes'])}),flush=True)
    if mode=='exit-before-cancel':self.assertFalse(row['cancel_descendant_cleanup_confirmed']);self.assertIn('direct helper',row['capture_joined_semantics'])
    else:
     self.assertLess(elapsed,3.4)
     live=subprocess.check_output(['ps','-axo','pid=,stat='],text=True)
     rows={int(x.split()[0]):x.split()[1] for x in live.splitlines() if len(x.split())==2}
     self.assertTrue(all(pid not in rows or rows[pid].startswith('Z') for pid in children),'observed child remained running after cancellation');self.assertTrue(row['cancel_descendant_cleanup_confirmed'])
   finally:
    for pid in children:
     try:os.kill(pid,signal.SIGKILL)
     except ProcessLookupError:pass
    try:os.killpg(p.pid,signal.SIGKILL)
    except ProcessLookupError:pass
    if p.poll() is None:p.wait(timeout=1)
 def test_term_exit_root_still_kills_observed_ignoreterm_child(self):self._case('term-exit')
 def test_preexited_root_reports_unconfirmed_not_full_cleanup(self):self._case('exit-before-cancel')
 def test_three_ignoreterm_roots_and_children_share_kill_budget(self):self._case('ignore-term')
if __name__=='__main__':unittest.main()
