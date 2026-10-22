from pathlib import Path
import json,subprocess,tempfile,unittest
P=Path(__file__).absolute().parent;B=P.parent/'full-original-distribution-exact79a-profile-v1/original-distribution-local.yaml';C=P.parent/'full-original-distribution-exact79a-rate500-profile-v2/original-distribution-local.yaml';HELPER=P.parent/'profile-rate500-proposal-v1/offline-parser'
def differences(a,b,path=''):
 if isinstance(a,dict) and isinstance(b,dict):
  return sum((differences(a[k],b[k],path+'/'+k) for k in sorted(set(a)|set(b))),[])
 if isinstance(a,list) and isinstance(b,list):
  assert len(a)==len(b);return sum((differences(x,y,path+'/'+str(i)) for i,(x,y) in enumerate(zip(a,b))),[])
 return [] if a==b else [(path,a,b)]
class Contract(unittest.TestCase):
 def test_real_loader_planner_two_derived_fields_and_original_receipt_control(self):
  r=subprocess.run([str(HELPER),str(B),str(C)],capture_output=True,text=True,timeout=10);self.assertEqual(r.returncode,0,r.stderr);x=json.loads(r.stdout);diff=differences(x['baseline']['config'],x['candidate']['config']);self.assertEqual({d[0]:(d[1],d[2]) for d in diff},{'/workload/max_global_burst':(4000,1000),'/workload/send_rate_per_second':(2000,500)});self.assertEqual(x['baseline']['run_hash'],x['candidate']['run_hash']);self.assertNotEqual(x['baseline']['assignment_hash'],x['candidate']['assignment_hash'])
  previous=json.loads((B.parent/'report/run-start.json').read_text());self.assertEqual(x['baseline']['run_hash'],previous['run_hash']);self.assertEqual(x['baseline']['assignment_hash'],previous['assignment_hash']);self.assertEqual(x['candidate']['assignment_count'],3)
  (P/'parser-output.json').write_text(json.dumps(x,indent=2)+'\n');(P/'typed-config-differences.json').write_text(json.dumps(diff,indent=2)+'\n')
 def test_strict_loader_rejects_unknown_key_and_zero_rate(self):
  for text in [C.read_text()+'unknown_probe_key: true\n',C.read_text().replace('send_rate_per_second: 500','send_rate_per_second: 0')]:
   d=Path(tempfile.mkdtemp(prefix='wk-rate500-parser-'));f=d/'invalid.yaml';f.write_text(text);r=subprocess.run([str(HELPER),str(B),str(f)],capture_output=True,text=True,timeout=10);self.assertNotEqual(r.returncode,0)
if __name__=='__main__':unittest.main(verbosity=2)
