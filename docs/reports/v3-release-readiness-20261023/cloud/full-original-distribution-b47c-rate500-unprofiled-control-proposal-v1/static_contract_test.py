import ast,hashlib,pathlib,tempfile,unittest
P=pathlib.Path(__file__).parent
class StaticControl(unittest.TestCase):
 def test_no_capture_or_sampler_dependency(self):
  launcher=(P/'launcher.proposed.sh').read_text();supervisor=(P/'supervise.proposed.py').read_text()
  for marker in ['PPROF_PID','HOT_PPROF','PROFILE_DRIVER_PID','profile-driver','trace?seconds','--helper','--sampler','LOCAL_PROFILE_LAUNCHER_SHA256','execution-trace-first-grant-proposal','profile-stop','SAMPLER_ROOT','SAMPLER_MANIFEST','SAMPLER_FILES']:
   self.assertNotIn(marker,launcher+supervisor)
 def fixture(self,root):
  tree=ast.parse((P/'supervise.proposed.py').read_text());functions=[x for x in tree.body if isinstance(x,ast.FunctionDef) and x.name=='verify_launch_inputs'];self.assertEqual(len(functions),1);ns={'Path':pathlib.Path,'hashlib':hashlib};exec(compile(ast.Module(body=functions,type_ignores=[]),'isolated-four-pin-gate','exec'),ns);out=root/'run';out.mkdir();inputs={}
  for name in ['launcher.sh','original-distribution-local.yaml','watchdog.py','verify-slots.py']:(out/name).write_text('synthetic '+name);inputs[name]=hashlib.sha256((out/name).read_bytes()).hexdigest()
  ns.update(OUT=out,INPUT_HASHES=inputs);return ns,out
 def test_valid_four_inputs_without_sampler_return_pinned_launcher(self):
  with tempfile.TemporaryDirectory() as d:
   ns,out=self.fixture(pathlib.Path(d));self.assertEqual(ns['verify_launch_inputs'](),ns['INPUT_HASHES']['launcher.sh'])
 def test_every_changed_input_rejected(self):
  for name in ['launcher.sh','original-distribution-local.yaml','watchdog.py','verify-slots.py']:
   with self.subTest(name=name),tempfile.TemporaryDirectory() as d:
    ns,out=self.fixture(pathlib.Path(d));(out/name).write_text('changed')
    with self.assertRaises(AssertionError):ns['verify_launch_inputs']()
if __name__=='__main__':unittest.main()
