import copy,hashlib,importlib.util,json,pathlib,signal,sys,tempfile,unittest
sys.dont_write_bytecode=True
p=pathlib.Path(__file__).with_name("PRIVATE_offline_db_inspect.proposed.py")
s=importlib.util.spec_from_file_location("guard",p);m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
class Guards(unittest.TestCase):
 def test_expected_inventory_sha_is_pinned(self):
  with tempfile.TemporaryDirectory() as d:
   p=pathlib.Path(d)/"inventory.json";p.write_text("{}\n");raw=p.read_bytes();self.assertEqual(m.load_expected_inventory(p,hashlib.sha256(raw).hexdigest()),{})
   with self.assertRaises(AssertionError):m.load_expected_inventory(p,"0"*64)
 def test_original_mtime_and_sha_must_match(self):
  old={"data/node1/messages/x":{"bytes":1,"sha256":"a","mtime_ns":5}}
  m.assert_original_inventory(copy.deepcopy(old),old)
  for k in ["mtime_ns","sha256"]:
   new=copy.deepcopy(old);new["data/node1/messages/x"][k]="different"
   with self.assertRaises(AssertionError):m.assert_original_inventory(new,old)
 def test_copy_uses_exact_size_hash_mapping(self):
  original={"data/node1/messages/x":{"bytes":1,"sha256":"a","mtime_ns":5}};good={"data/node1/messages/x":{"bytes":1,"sha256":"a"}}
  m.assert_copy_inventory(good,original)
  for bad in [{},{"extra":{"bytes":1,"sha256":"a"}},{"data/node1/messages/x":{"bytes":2,"sha256":"a"}},{"data/node1/messages/x":{"bytes":1,"sha256":"b"}}]:
   with self.assertRaises(AssertionError):m.assert_copy_inventory(bad,original)
 def test_outer_deadline_kills_and_joins_owned_sleeper(self):
  cmd=[sys.executable,"-c","import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);time.sleep(5)"]
  r=m.run_owned(cmd,total_seconds=.6,work_seconds=.15,term_grace=.1,kill_grace=.1)
  self.assertTrue(r["timed_out"]);self.assertTrue(r["child_joined"]);self.assertFalse(r["owned_group_alive"]);self.assertTrue(r["kill_sent"]);self.assertLess(r["elapsed_seconds"],.6)
if __name__=="__main__":unittest.main()
