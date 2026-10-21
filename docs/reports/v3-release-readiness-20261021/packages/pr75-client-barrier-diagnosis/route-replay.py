"""Deterministic actual-route and actual-receipt replay, not a native signed run."""
import hashlib,importlib.util,json,sys,tempfile
from pathlib import Path
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-object-delivery')
def load(path,name):
 s=importlib.util.spec_from_file_location(name,ROOT/path);m=importlib.util.module_from_spec(s);sys.modules[name]=m;s.loader.exec_module(m);return m
D=load('tests/native_stable_clients_drill.py','barrier_replay_driver')
V=load('scripts/validate-production-package-clients.py','barrier_replay_validator')
trace=json.loads((Path(__file__).parent.parent/'pr75-ci-generation-1/first-failure.json').read_text())['http_requests']
with tempfile.TemporaryDirectory() as tmp:
 root=Path(tmp);maps={'A':{},'B':{}}
 # Use the exact captured URI ordering. Marker bytes are deliberately not RPMs:
 # the receipt checks a digest only, and this probe does not claim signature work.
 a_prefix=next(r['path'].split('site/rpm/')[0] for r in trace if '/r4242-' in r['path'])
 b_prefix=next(r['path'].split('site/rpm/')[0] for r in trace if '/r4243-' in r['path'])
 repo='site/rpm/stable/el/9/x86_64/'
 for gen,prefix in [('A',a_prefix),('B',b_prefix)]:
  file=root/('mirror'+gen);file.write_text(D.ORIGIN+prefix+repo+'\n');maps[gen][D.MIRROR]={'file':file.name}
 for path in sorted({r['path'] for r in trace if r['path']!=D.MIRROR}):
  name='object'+str(len(list(root.iterdir())));data=(('A' if '/r4242-' in path else 'B')+' '+path.split('/')[-1]).encode()
  (root/name).write_bytes(data);maps['B'][path]={'file':name}
  if '/r4242-' in path:maps['A'][path]={'file':name}
 a_product=a_prefix+repo+'Packages/wukongim_3.0.0_linux_amd64.rpm';b_product=b_prefix+repo+'Packages/wukongim_3.0.1_linux_amd64.rpm'
 (root/'a-product').write_bytes(b'A retained product marker');maps['A'][a_product]={'file':'a-product'};maps['B'][a_product]={'file':'a-product'}
 maps['B'][b_prefix+repo+'Packages/wukongim_3.0.0_linux_amd64.rpm']={'file':'a-product'}
 route=D.RouteTable(root,maps);route.configure({'serial':35,'generation':'A','mode':'rpm-switch'})
 observed=[]
 for r in trace:
  status,data,headers=route.response(r['path'],{})
  assert status==200
  observed.append({'path':r['path'],'status':status,'selected_generation_after_response':route.generation,'body_marker':data.decode()})
 output=root/'downloads';output.mkdir();(output/'wukongim_3.0.1_linux_amd64.rpm').write_bytes(data)
 expected={hashlib.sha256(b'A retained product marker').hexdigest():['3.0.0']}
 reason=None
 try:V._download_receipt(output,'rpm',expected)
 except V.ClientValidationError as error:reason=str(error)
 assert reason=='downloaded rpm package is absent from the indexed snapshot'
 assert observed[0]['body_marker'].startswith(D.ORIGIN+a_prefix) and observed[0]['selected_generation_after_response']=='B'
 assert any(item['path'].endswith('primary.xml.gz') and '/r4243-' in item['path'] for item in observed)
 print(json.dumps({'replayed_captured_request_count':len(trace),'host_exact_A_rejection':reason,'switch_after_first_mirror':True,'observed':observed,'scope':'Real RouteTable.response and production _download_receipt with marker bytes. Native resolution/signatures are from CI evidence, not reproduced here.'},indent=2))
 if '--assert-exact-A' in sys.argv:raise SystemExit(1)
