import ast,hashlib,pathlib,tempfile,unittest
PROPOSAL=pathlib.Path(__file__).parent
class InputGuard(unittest.TestCase):
 def fixture(self,root):
  tree=ast.parse((PROPOSAL/'supervise.proposed.py').read_text());functions=[n for n in tree.body if isinstance(n,ast.FunctionDef) and n.name=='verify_launch_inputs'];self.assertEqual(len(functions),1,'trusted launch input gate missing')
  namespace={'Path':pathlib.Path,'hashlib':hashlib};exec(compile(ast.Module(body=functions,type_ignores=[]),'isolated-fixed-input-gate','exec'),namespace)
  out=root/'run';sampler=root/'sampler';out.mkdir();sampler.mkdir();inputs={}
  for name in ['launcher.sh','original-distribution-local.yaml','watchdog.py','verify-slots.py']:(out/name).write_text('synthetic '+name);inputs[name]=hashlib.sha256((out/name).read_bytes()).hexdigest()
  members={}
  for name in ['helper.sh','watcher.py']:(sampler/name).write_text('synthetic '+name);members[name]=hashlib.sha256((sampler/name).read_bytes()).hexdigest()
  (sampler/'PUBLIC_SHA256SUMS').write_text(''.join(digest+'  '+name+'\n' for name,digest in sorted(members.items())))
  namespace.update(OUT=out,INPUT_HASHES=inputs,SAMPLER_ROOT=sampler,SAMPLER_MANIFEST_SHA=hashlib.sha256((sampler/'PUBLIC_SHA256SUMS').read_bytes()).hexdigest(),SAMPLER_FILES=members)
  return namespace,out,sampler
 def test_valid_inputs_return_only_pinned_launcher_hash(self):
  with tempfile.TemporaryDirectory() as d:
   ns,out,sampler=self.fixture(pathlib.Path(d));self.assertEqual(ns['verify_launch_inputs'](),ns['INPUT_HASHES']['launcher.sh'])
 def test_launcher_member_and_manifest_drift_rejected(self):
  for kind in ['launcher','sampler_member','sampler_manifest']:
   with self.subTest(kind=kind),tempfile.TemporaryDirectory() as d:
    ns,out,sampler=self.fixture(pathlib.Path(d));path={'launcher':out/'launcher.sh','sampler_member':sampler/'helper.sh','sampler_manifest':sampler/'PUBLIC_SHA256SUMS'}[kind];path.write_text('altered')
    with self.assertRaises(AssertionError):ns['verify_launch_inputs']()
if __name__=='__main__':unittest.main()
