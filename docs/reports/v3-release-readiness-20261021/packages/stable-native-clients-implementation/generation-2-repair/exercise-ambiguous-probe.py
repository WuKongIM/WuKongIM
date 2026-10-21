from pathlib import Path
import importlib.util, json, subprocess, tempfile, types
from unittest import mock
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-stable-native-clients')
OUT=Path(__file__).parent
spec=importlib.util.spec_from_file_location('review_drill',ROOT/'tests/native_stable_clients_drill.py')
d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)
label='0123456789abcdef0123456789abcdef'; identifier='a'*64; sentinel='f'*64
resources={sentinel:{'label':'outside','name':'external-sentinel','kind':'container'}}
created=[]; cleanup_calls=[]
def fake_run(command, **kwargs):
 cleanup_calls.append(command)
 if command[1:3]==['rm','--force']:
  assert command[-1]!=sentinel
  resources.pop(command[-1]); raw=b''
 elif 'inspect' in command:
  item=resources.get(command[-1])
  if not item:return subprocess.CompletedProcess(command,1,b'',b'No such container')
  raw=f"{command[-1]} /{item['name']} {item['label']}\n".encode()
 else:
  assert 'label=wk-stable-client-run='+label in command
  kind='container' if command[1]=='ps' else 'network'
  raw=''.join(key+'\n' for key,item in resources.items() if item['kind']==kind and item['label']==label).encode()
 return subprocess.CompletedProcess(command,0,raw,b'')
def fake_command(evidence,stage,command,**kwargs):
 if stage=='create-container':
  item={'label':label,'name':command[command.index('--name')+1],'kind':'container'}
  resources[identifier]=item;created.append(item)
  assert item['name'] in owned.attempts['containers']
  raise RuntimeError('simulated TERM after daemon creation before client ID capture')
 return subprocess.CompletedProcess(command,0,b'public probe\n',b'')
def fake_build(args,scratch,evidence):
 p=scratch/'binaries';p.mkdir();return p
with tempfile.TemporaryDirectory() as tmp:
 p=Path(tmp);out=p/'out';out.mkdir();owned=d.OwnedDocker(label,run=fake_run);evidence={'commands':[]}
 args=types.SimpleNamespace(product_root=p,trusted_root=p,output_dir=out)
 with mock.patch.object(d.sys,'platform','linux'),mock.patch.object(d.shutil,'which',return_value='/public/tool'),mock.patch.object(d,'run_command',side_effect=fake_command),mock.patch.object(d,'build_product',side_effect=fake_build):
  try:d.exercise(args,evidence,owned)
  except RuntimeError as e:error=str(e)
 assert error==evidence['exercise_failure']=='simulated TERM after daemon creation before client ID capture'
 assert len(created)==1 and set(resources)=={sentinel} and owned.cleanup()['verified']
 assert evidence['reader_cleanup']['verified'] and len(cleanup_calls)>=8
 result={'scope':'Actual exercise/create/cleanup; native commands/build/platform substituted, no Docker/GPG/native execution.', 'failure_preserved':error,'creation':created,'outside_sentinel_preserved':True,'cleanup_commands':cleanup_calls,'evidence':evidence}
 (OUT/'08-exercise-ambiguous-result.json').write_text(json.dumps(result,indent=2)+'\n')
 print('Actual exercise ambiguous-create probe passed; original failure preserved, owned resource removed, outside sentinel retained.')
