import gzip,hashlib,importlib.util,json,pathlib,tempfile,unittest
from unittest import mock
SPEC=importlib.util.spec_from_file_location('offline_adapter',pathlib.Path(__file__).with_name('offline_adapter.py'))
A=importlib.util.module_from_spec(SPEC);SPEC.loader.exec_module(A)
P='github.com/WuKongIM/WuKongIM/'
def vint(n):
 b=bytearray()
 while n>127:b.append((n&127)|128);n>>=7
 b.append(n);return bytes(b)
def fld(k,v):return vint(k*8+2)+vint(len(v))+v
def vi(k,v):return vint(k*8)+vint(v)
def fixture(kind='goroutine',value=7):
 strings=['',kind,'count' if kind=='goroutine' else 'nanoseconds','synthetic.fn','synthetic.go','private-fixture-uid']
 b=b''.join(fld(6,s.encode()) for s in strings)
 b+=fld(1,vi(1,1)+vi(2,2))
 if kind=='cpu':b+=fld(1,vi(1,1)+vi(2,2))
 b+=fld(5,vi(1,1)+vi(2,3)+vi(4,4));b+=fld(4,vi(1,1)+fld(4,vi(1,1)+vi(2,10)))
 values=[value] if kind=='goroutine' else [2,value]
 b+=fld(2,fld(1,vint(1))+fld(2,b''.join(vint(x) for x in values))+fld(3,vi(1,5)+vi(2,5)))
 b+=vi(9,1792712367000000000)+vi(10,10_000_000_000)
 return gzip.compress(b)
def stack(n,*frames):return {'values':[n],'frames':[{'function':P+fn if not fn.startswith('sync.') else fn,'file':'synthetic.go','line':ln} for fn,ln in frames]}
class Adapter(unittest.TestCase):
 def make(self,d,body=None,kind='goroutine'):
  path=pathlib.Path(d)/f'node-1-{kind}.pb.gz';path.write_bytes(fixture(kind) if body is None else body);s=path.stat();r={'node_id':1,'kind':kind,'path':str(path),'bytes':s.st_size,'sha256':hashlib.sha256(path.read_bytes()).hexdigest(),'mtime_ns':s.st_mtime_ns};return path,r
 def test_approved_six_paths(self):
  paths=A.fixed_paths(pathlib.Path('/approved'));self.assertEqual(len(paths),6);self.assertEqual(paths[(3,'goroutine')],pathlib.Path('/approved/profiles-three-node/node-3/profiles/node-3-goroutine.pb.gz'))
 def test_valid_path_and_profile(self):
  with tempfile.TemporaryDirectory() as d:
   p,r=self.make(d);self.assertEqual(A.verify_input(r,{(1,'goroutine'):p})['sha256'],r['sha256']);meta,rows=A.load_profile(p);A.verify_types('goroutine',meta,rows);self.assertEqual(rows[0]['values'],[7])
 def test_unapproved_path(self):
  with tempfile.TemporaryDirectory() as d:
   p,r=self.make(d)
   with self.assertRaisesRegex(A.AdapterError,'unapproved_path'):A.verify_input(r,{})
 def test_missing(self):
  with tempfile.TemporaryDirectory() as d:
   p,r=self.make(d);p.unlink()
   with self.assertRaisesRegex(A.AdapterError,'missing'):A.verify_input(r,{(1,'goroutine'):p})
 def test_symlink(self):
  with tempfile.TemporaryDirectory() as d:
   p,r=self.make(d);q=p.with_suffix('.real');p.rename(q);p.symlink_to(q)
   with self.assertRaisesRegex(A.AdapterError,'unsafe_symlink'):A.verify_input(r,{(1,'goroutine'):p})
 def test_bytes_sha_mtime_mismatch(self):
  with tempfile.TemporaryDirectory() as d:
   p,r=self.make(d)
   for key,value in [('bytes',r['bytes']+1),('sha256','0'*64),('mtime_ns',r['mtime_ns']+1)]:
    with self.subTest(key=key),self.assertRaisesRegex(A.AdapterError,'input_mismatch'):A.verify_input({**r,key:value},{(1,'goroutine'):p})
 def test_compressed_cap(self):
  with tempfile.TemporaryDirectory() as d:
   p,r=self.make(d,b'X'*(4194304+1))
   with self.assertRaisesRegex(A.AdapterError,'input_oversized'):A.verify_input(r,{(1,'goroutine'):p})
 def test_decompressed_cap(self):
  with tempfile.TemporaryDirectory() as d:
   p,r=self.make(d,gzip.compress(b'X'*(4194304+1)))
   with self.assertRaises(AssertionError):A.load_profile(p)
 def test_bad_gzip_and_truncated_protobuf(self):
  with tempfile.TemporaryDirectory() as d:
   for body in [b'not-gzip',gzip.compress(b'\x80')]:
    p,r=self.make(d,body)
    with self.subTest(body=len(body)),self.assertRaises(Exception):A.load_profile(p)
 def test_wrong_type(self):
  with self.assertRaisesRegex(A.AdapterError,'sample_type_mismatch'):A.verify_types('goroutine',{'types':[{'type':'cpu','unit':'nanoseconds'}]},[])
 def test_bad_sample_shape(self):
  with self.assertRaisesRegex(A.AdapterError,'sample_shape'):A.verify_types('goroutine',{'types':[{'type':'goroutine','unit':'count'}]},[{'values':[],'frames':[]}])
 def test_identity_labels_not_decoded(self):
  with tempfile.TemporaryDirectory() as d:
   p,r=self.make(d);m,rows=A.load_profile(p);self.assertNotIn('private-fixture-uid',json.dumps([m,rows]));self.assertEqual(rows[0]['frames'][0]['function'],'synthetic.fn')
 def test_fresh_counts_without_old_assertions(self):
  rows=[stack(7,('internal/runtime/delivery.(*Runtime).runWorker',1),('internal/runtime/delivery.(*orderedPlanQueue).dequeue',1))]
  s=A.classify_goroutine({'types':[{'type':'goroutine','unit':'count'}]},rows);self.assertEqual(s['delivery_runWorker_total'],7);self.assertEqual(s['delivery_runWorker_disjoint']['idle_plan_dequeue'],7);self.assertEqual(s['recoverPage_total'],0)
 def test_disjoint_precedence_and_overlap(self):
  rows=[stack(3,('internal/runtime/delivery.(*Runtime).runWorker',1),('internal/runtime/delivery.(*orderedPlanQueue).dequeue',1),('internal/usecase/presence.(*AuthorityRecovery).recoverPage',1),('internal/usecase/presence.(*AuthorityRecovery).admitRecoveryPage',156))]
  s=A.classify_goroutine({},rows);self.assertEqual(s['delivery_runWorker_disjoint']['idle_plan_dequeue'],3);self.assertEqual(s['recoverPage_disjoint']['registry_quota_wait'],3);self.assertEqual(s['delivery_runWorker_total'],3)
 def test_unknown_kept(self):
  rows=[stack(2,('internal/runtime/delivery.(*Runtime).runWorker',1),('synthetic.unknown',1))];s=A.classify_goroutine({},rows);self.assertEqual(s['delivery_runWorker_disjoint']['unclassified'],2)
 def test_owner_holder_and_rpc_cohorts_overlap(self):
  rows=[stack(2,('internal/runtime/delivery.(*Runtime).runWorker',1),('internal/usecase/presence.(*AuthorityRecovery).executeRecoveryCohort',1),('internal/infra/cluster.(*PresenceRecoveryOwners).RecoverRoutesByTargets',1),('internal/access/node.(*Client).ReadOwnerRoutesByTargets',1),('pkg/transport/internal/conn.(*Conn).Call',207))]
  s=A.classify_goroutine({},rows);self.assertEqual(s['actual_owner_read_holder_total'],2);self.assertEqual(s['actual_owner_read_operation_disjoint']['batch_ReadOwnerRoutesByTargets'],2);self.assertEqual(s['Conn_Call_total'],2)
 def test_cpu_weights(self):
  s=A.classify_cpu({},[{'values':[2,20],'frames':[{'function':'synthetic.cpu','file':'synthetic.go','line':1}]}]);self.assertEqual(s['cpu_sample_count'],2);self.assertEqual(s['cpu_total_sample_nanoseconds'],20)
 def test_public_size_bound_and_closed_error(self):
  with self.assertRaisesRegex(A.AdapterError,'output_oversized'):A.bounded_json({'x':'x'*100},20)
  self.assertEqual(A.closed_error(ValueError('private identity')),'ValueError');self.assertNotIn('private',A.closed_error(ValueError('private identity')))
if __name__=='__main__':unittest.main()
