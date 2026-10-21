from pathlib import Path
import importlib.util,json,subprocess
root=Path(__file__).parent
spec=importlib.util.spec_from_file_location('frozen_v3_network',root/'source/tests/native_stable_clients_drill.py');d=importlib.util.module_from_spec(spec);spec.loader.exec_module(d)
identifier='a'*64;name='wk-scl-frozen-v2';label='frozen-v2';exists=True;calls=[]
def run(command,**kwargs):
 global exists
 calls.append(command)
 if command[:3]==['docker','network','rm']:
  exists=False;return subprocess.CompletedProcess(command,0,b'',b'')
 if 'inspect' in command:
  return subprocess.CompletedProcess(command,0,(identifier+' '+name+' '+label+'\n').encode(),b'') if exists else subprocess.CompletedProcess(command,1,b'',('Error response from daemon: network '+identifier+' not found').encode())
 return subprocess.CompletedProcess(command,0,(identifier+'\n').encode() if exists and command[:3]==['docker','network','ls'] else b'',b'')
owned=d.OwnedDocker(label,run=run)
owned.create('network',['docker','network','create','--label','wk-stable-client-run='+label,name],lambda *a:identifier.encode())
first=owned.cleanup(final=False);final=owned.cleanup()
assert first['verified'] and final['verified'] and not exists
assert sum('inspect' in command for command in calls)==1
assert sum('--filter' in command for command in calls)==8
assert owned.network is None and name in final['creation_attempts']['networks']
assert final['errors']==[]
result={'first':first,'final':final,'network_removed':not exists,'commands':calls,'basis':'Frozen v3 actual cleanup code; Docker native command replaced with root-reported actual missing-network stderr form. No Docker invoked here.'}
(root/'network-repeat-result.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
