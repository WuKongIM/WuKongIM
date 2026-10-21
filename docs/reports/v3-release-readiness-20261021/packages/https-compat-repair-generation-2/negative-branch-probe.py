"""Bounded control probe: execute the actual negative-download branch AST."""
import ast,importlib.util,json,subprocess,sys
from pathlib import Path
source=Path(sys.argv[1])
spec=importlib.util.spec_from_file_location('drill_under_probe',source)
drill=importlib.util.module_from_spec(spec);spec.loader.exec_module(drill)
parsed=ast.parse(source.read_text())
download=next(n for n in ast.walk(parsed) if isinstance(n,ast.FunctionDef) and n.name=='download')
branch=next(n for n in download.body if isinstance(n,ast.If) and isinstance(n.test,ast.Name) and n.test.id=='expected_failure')
function=ast.parse('def probe():\n pass').body[0];function.body=[branch]
unit=ast.fix_missing_locations(ast.Module(body=[function],type_ignores=[]))
observed=[{'path':drill.INRELEASE,'status':200,'sha256':'0'*64}]
values=dict(drill.__dict__,expected_failure=True,result=subprocess.CompletedProcess([],100,b'SSL transport failure',b''),wrong_ca=False,number=1,requests=lambda _:observed)
exec(compile(unit,str(source),'exec'),values)
results=[]
for mode in ('missing-byhash','corrupt-byhash'):
 values['mode']=mode
 try:
  value=values['probe'](); results.append({'mode':mode,'rejected':False,'accepted':value})
 except RuntimeError as error:
  results.append({'mode':mode,'rejected':True,'error':str(error)})
print(json.dumps({'scope':'actual negative branch control flow; no Docker/native client','request_receipt':observed,'results':results},sort_keys=True,indent=2))
assert all(r['rejected'] for r in results),'accepted unrelated SSL failure without any by-hash fault response'
