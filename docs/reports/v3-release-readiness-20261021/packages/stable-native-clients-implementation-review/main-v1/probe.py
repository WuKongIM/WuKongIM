from pathlib import Path
import ast,hashlib,importlib.util,json,subprocess,tempfile,types
from unittest import mock
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-stable-native-clients')
OUT=Path(__file__).parent
spec=importlib.util.spec_from_file_location('review_drill',ROOT/'tests/native_stable_clients_drill.py')
d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)
created=[]; cleanup_calls=[]
def fake_run(command, **kwargs):
 cleanup_calls.append(command)
 return subprocess.CompletedProcess(command,0,('a'*64+'\n').encode() if command[:3]==['docker','ps','-aq'] else b'',b'')
def fake_command(evidence,stage,command,**kwargs):
 if stage=='create-container':
  created.append({'id':'a'*64,'name':command[command.index('--name')+1],'label':command[command.index('--label')+1]})
  raise RuntimeError('simulated TERM after daemon creation before client ID capture')
 return subprocess.CompletedProcess(command,0,b'public probe\n',b'')
def fake_build(args,scratch,evidence):
 p=scratch/'binaries';p.mkdir();return p
with tempfile.TemporaryDirectory() as tmp:
 p=Path(tmp); out=p/'out';out.mkdir()
 owned=d.OwnedDocker('review-uncertain-create',run=fake_run)
 evidence={'commands':[]}
 args=types.SimpleNamespace(product_root=p,trusted_root=p,output_dir=out)
 with mock.patch.object(d.sys,'platform','linux'),mock.patch.object(d.shutil,'which',return_value='/public/tool'),mock.patch.object(d,'run_command',side_effect=fake_command),mock.patch.object(d,'build_product',side_effect=fake_build):
  try:d.exercise(args,evidence,owned)
  except RuntimeError as e: error=str(e)
 result={'scope':'Actual exercise/create/cleanup code; only native commands, build and platform are substituted. No Docker or GPG invoked.',
         'failure':error,'daemon_created':created,'recorded_ids':sorted(owned.containers),'cleanup_commands':cleanup_calls,'cleanup':owned.cleanup()}
 assert len(created)==1 and not cleanup_calls and result['cleanup']['verified'] is True
 (OUT/'uncertain-create-result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result,indent=2))
base=subprocess.run(['git','show','335cd256:scripts/validate-production-package-clients.py'],cwd=ROOT,check=True,capture_output=True,text=True).stdout
current=(ROOT/'scripts/validate-production-package-clients.py').read_text()
old={n.name:n for n in ast.parse(base).body if isinstance(n,(ast.FunctionDef,ast.ClassDef))}
new={n.name:n for n in ast.parse(current).body if isinstance(n,(ast.FunctionDef,ast.ClassDef))}
changed=[name for name in old if ast.dump(old[name])!=ast.dump(new[name])]
assert changed==['validate_clients','build_parser','main'],changed
# Default preview executable body and all other existing helper implementations remain identical.
old_body=old['validate_clients'].body
new_body=new['validate_clients'].body
assert ast.dump(ast.Module(body=old_body,type_ignores=[]))==ast.dump(ast.Module(body=new_body[3:],type_ignores=[]))
(OUT/'preview-ast-result.json').write_text(json.dumps({'changed_existing_functions':changed,'all_previous_helpers_ast_equal':True,'default_preview_body_ast_equal':True},indent=2)+'\n')
