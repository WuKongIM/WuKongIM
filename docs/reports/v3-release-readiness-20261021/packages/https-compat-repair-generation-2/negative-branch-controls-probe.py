"""Execute the real rejection branch with valid fault receipts and bad exits."""
import ast,hashlib,importlib.util,json,subprocess,sys
from pathlib import Path
source=Path(sys.argv[1])
spec=importlib.util.spec_from_file_location('drill_under_probe',source)
drill=importlib.util.module_from_spec(spec);spec.loader.exec_module(drill)
parsed=ast.parse(source.read_text())
download=next(n for n in ast.walk(parsed) if isinstance(n,ast.FunctionDef) and n.name=='download')
branch=next(n for n in download.body if isinstance(n,ast.If) and isinstance(n.test,ast.Name) and n.test.id=='expected_failure')
function=ast.parse('def probe():\n pass').body[0];function.body=[branch]
unit=ast.fix_missing_locations(ast.Module(body=[function],type_ignores=[]))
values=dict(drill.__dict__,expected_failure=True,wrong_ca=False,number=1)
exec(compile(unit,str(source),'exec'),values)
results=[]
for mode in ('missing-byhash','corrupt-byhash'):
 observed=[{'path':'/apt/dists/stable/main/binary-amd64/by-hash/SHA512/'+'a'*128,
 'status':404 if mode=='missing-byhash' else 200,'sha256':hashlib.sha256(drill.CORRUPT_APT_INDEX).hexdigest()}]
 values.update(mode=mode,requests=lambda _:observed)
 for code,text,accept in [(100,b'Failed to fetch authenticated index',True),
   *[(code,b'Failed to fetch authenticated index',False) for code in (0,124,125,126,127,137,143)],
   (100,b'command not found',False),(100,b'cryptic non-transport error',False)]:
  values['result']=subprocess.CompletedProcess([],code,text,b'')
  try: value=values['probe']();accepted=True
  except RuntimeError: accepted=False
  results.append({'mode':mode,'exit':code,'diagnostic':text.decode(),'accepted':accepted,'expected':accept})
print(json.dumps({'scope':'actual negative branch control flow; no Docker/native client','results':results},sort_keys=True,indent=2))
assert all(r['accepted']==r['expected'] for r in results)
