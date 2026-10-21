"""Bounded actual negative branch: completed-write flag AND specific native diagnostic."""
import ast,hashlib,importlib.util,json,subprocess
from pathlib import Path
source=Path(__file__).parent/'files/tests/native_stable_clients_drill.py'
spec=importlib.util.spec_from_file_location('drill',source);d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)
download=next(n for n in ast.walk(ast.parse(source.read_text())) if isinstance(n,ast.FunctionDef) and n.name=='download')
branch=next(n for n in download.body if isinstance(n,ast.If) and isinstance(n.test,ast.Name) and n.test.id=='expected_failure')
f=ast.FunctionDef(name='probe',args=ast.arguments(posonlyargs=[],args=[],kwonlyargs=[],kw_defaults=[],defaults=[]),body=[branch],decorator_list=[])
results=[]
for algorithm in ('sha256','sha512'):
 for mode in ('missing-byhash','corrupt-byhash'):
  receipt={'path':'/apt/dists/stable/main/binary-amd64/by-hash/'+algorithm.upper()+'/'+hashlib.new(algorithm,b'index').hexdigest(),'status':404 if mode=='missing-byhash' else 200,'sha256':hashlib.sha256(b'' if mode=='missing-byhash' else d.CORRUPT_APT_INDEX).hexdigest()}
  for completed in (None,False,True):
   for diagnostic in ('SSL connection failed before response','Failed to fetch authenticated index','Hash Sum mismatch','404 Not Found'):
    item=dict(receipt)
    if completed is not None:item['response_write_completed']=completed
    values=dict(d.__dict__,expected_failure=True,result=subprocess.CompletedProcess([],100,diagnostic.encode(),b''),wrong_ca=False,number=1,requests=lambda _:[item],mode=mode)
    exec(compile(ast.fix_missing_locations(ast.Module(body=[f],type_ignores=[])),str(source),'exec'),values)
    try: values['probe']();accepted=True
    except RuntimeError:accepted=False
    expected=completed is True and (diagnostic=='Hash Sum mismatch' or (mode=='missing-byhash' and diagnostic=='404 Not Found'))
    assert accepted==expected,(algorithm,mode,completed,diagnostic,accepted)
    results.append({'algorithm':algorithm,'mode':mode,'completed':completed,'diagnostic':diagnostic,'accepted':accepted,'expected':expected})
print(json.dumps({'actual_branch':True,'network_native_execution':False,'cases':len(results),'results':results},indent=2))
