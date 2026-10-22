from pathlib import Path
import json,subprocess,sys,tempfile,unittest
BASE=Path(__file__).absolute().parent
class Contract(unittest.TestCase):
 def test_exact_metadata_guard_and_source_receipt_hash_refusals(self):
  path=BASE/'exact_build_guard.py'
  result=subprocess.run([sys.executable,str(path)],capture_output=True,text=True,timeout=10)
  self.assertEqual(result.returncode,0,result.stderr);self.assertTrue(json.loads(result.stdout)['verified']);self.assertFalse(json.loads(result.stdout)['product_started'])
  source=path.read_text()
  for before,after in [('EXACT_SERVER_SHA = "5b30743481398ad8175476d18ffbc676c6da5288657919ae8e704324e67351ab"','EXACT_SERVER_SHA = "'+'0'*64+'"'),('SOURCE = "79a7177032a6ae1ec7015cefe0e6f94eb0f19f06"','SOURCE = "'+'0'*40+'"'),('completed.json','missing-receipt.json')]:
   with self.subTest(mutated=before):
    self.assertIn(before,source);d=Path(tempfile.mkdtemp(prefix='wk-exact-guard-fake-'));copy=d/'guard.py';copy.write_text(source.replace(before,after));r=subprocess.run([sys.executable,str(copy)],capture_output=True,text=True,timeout=10);self.assertNotEqual(r.returncode,0)
if __name__=='__main__':unittest.main(verbosity=2)
