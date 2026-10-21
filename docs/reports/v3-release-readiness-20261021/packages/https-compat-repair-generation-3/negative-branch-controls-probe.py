"""Execute real rejection control flow, including post-write wrong diagnostics."""
import ast,hashlib,importlib.util,json,subprocess,sys,types
from pathlib import Path
source=Path(sys.argv[1]);drill=types.ModuleType('drill_under_probe');drill.__file__=str(source)
exec(compile(source.read_bytes(),str(source),'exec'),drill.__dict__)
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
 'status':404 if mode=='missing-byhash' else 200,'sha256':hashlib.sha256(drill.CORRUPT_APT_INDEX).hexdigest(),
 'response_write_completed':True}]
 values.update(mode=mode,requests=lambda _:observed)
 for code,text,accept in [(100,b'Hash Sum mismatch',True), (100,b'File has unexpected size (28 != 605)',True),
   (100,b'Size mismatch',True),(100,b'404 Not Found',mode=='missing-byhash'),
   *[(code,b'Hash Sum mismatch',False) for code in (0,124,125,126,127,137,143)],
   (100,b'command not found',False),(100,b'cryptic non-transport error',False),
   (100,b'OpenSSL error: unexpected eof while reading',False),(100,b'Failed to fetch authenticated index',False)]:
  values['result']=subprocess.CompletedProcess([],code,text,b'')
  try: value=values['probe']();accepted=True
  except RuntimeError: accepted=False
  results.append({'mode':mode,'exit':code,'diagnostic':text.decode(),'accepted':accepted,'expected':accept})
print(json.dumps({'scope':'actual negative branch control flow with completed-write receipts; no native execution','results':results},sort_keys=True,indent=2))
assert all(r['accepted']==r['expected'] for r in results)
