"""No socket/key/native client: actual frozen handler and rejection branch control probe."""
import ast,hashlib,importlib.util,json,subprocess,tempfile,threading
from pathlib import Path
source=Path(__file__).parent/'files/tests/native_stable_clients_drill.py'
spec=importlib.util.spec_from_file_location('frozen_drill',source);d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)
parsed=ast.parse(source.read_text()); handler=next(n for n in ast.walk(parsed) if isinstance(n,ast.ClassDef) and n.name=='Handler')
get=next(n for n in handler.body if isinstance(n,ast.FunctionDef) and n.name=='do_GET')
download=next(n for n in ast.walk(parsed) if isinstance(n,ast.FunctionDef) and n.name=='download')
branch=next(n for n in download.body if isinstance(n,ast.If) and isinstance(n.test,ast.Name) and n.test.id=='expected_failure')
f=ast.FunctionDef(name='probe',args=ast.arguments(posonlyargs=[],args=[],kwonlyargs=[],kw_defaults=[],defaults=[]),body=[branch],decorator_list=[])
results=[]
for mode in ('missing-byhash','corrupt-byhash'):
 with tempfile.TemporaryDirectory() as temp:
  root=Path(temp);path='/apt/dists/stable/main/binary-amd64/by-hash/SHA512/'+'a'*128
  (root/'body').write_bytes(b'original');(root/'state.json').write_text(json.dumps({'serial':1,'generation':'A','mode':mode}))
  routes=d.RouteTable(root,{'A':{path:{'file':'body'}},'B':{}})
  sent=[]
  class Transport:
   headers={};server=type('Server',(),{'count':0})()
   def send_response(self,status):
    raise BrokenPipeError('injected disconnect before any response header/body write')
  transport=Transport();transport.path=path
  values=dict(d.__dict__,root=root,routes=routes)
  exec(compile(ast.fix_missing_locations(ast.Module(body=[get],type_ignores=[])),str(source),'exec'),values)
  values['do_GET'](transport)
  observed=[json.loads(line) for line in (root/'requests.jsonl').read_text().splitlines()]
  values.update(expected_failure=True,result=subprocess.CompletedProcess([],100,b'SSL connection failed before response',b''),wrong_ca=False,number=1,requests=lambda _:observed,mode=mode)
  exec(compile(ast.fix_missing_locations(ast.Module(body=[f],type_ignores=[])),str(source),'exec'),values)
  try:accepted=values['probe']();error=None
  except RuntimeError as exc:accepted=None;error=str(exc)
  results.append({'mode':mode,'actual_handler_failure':'BrokenPipeError before response headers/body','server_error_file':(root/'server-error').exists(),'logged':observed,'client_diagnostic':'SSL connection failed before response','negative_case_accepted':accepted is not None,'returned':accepted,'rejection':error})
print(json.dumps({'scope':'bounded actual handler + branch control flow; no network/native execution','results':results},indent=2))
