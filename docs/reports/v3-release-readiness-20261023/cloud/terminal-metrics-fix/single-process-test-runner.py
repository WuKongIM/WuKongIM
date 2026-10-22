import importlib.util,json,unittest,sys,os
from pathlib import Path
s=Path.cwd();spec=importlib.util.spec_from_file_location('fixture',s/'scripts/testdata/chat_lifecycle_terminal_evidence_test.py');m=importlib.util.module_from_spec(spec);sys.modules[spec.name]=m;spec.loader.exec_module(m)
suite=unittest.defaultTestLoader.loadTestsFromName('CollectorTests.test_registered_resource_families_survive_the_real_collector',m)
r=unittest.TextTestRunner(verbosity=2).run(suite)
Path(os.environ['TERMINAL_SINGLE_ARTIFACT']).write_text(json.dumps({'tests_run':r.testsRun,'failures':len(r.failures),'errors':len(r.errors),'cases':m.RESULTS},indent=2)+'\n')
sys.exit(not r.wasSuccessful())
