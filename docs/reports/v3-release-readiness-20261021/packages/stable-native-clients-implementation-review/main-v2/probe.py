from pathlib import Path
import importlib.util,subprocess,tempfile,json,hashlib,types
from unittest import mock
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-stable-native-clients')
OUT=Path(__file__).parent
s=importlib.util.spec_from_file_location('review_v2_drill',ROOT/'tests/native_stable_clients_drill.py'); d=importlib.util.module_from_spec(s);s.loader.exec_module(d)
class Daemon:
 def __init__(self): self.resources={'f'*64:{'kind':'container','name':'outside','label':'outside'}};self.commands=[];self.count=0
 def create(self, command, *, interrupt=False):
  self.count+=1; identifier=('%064x'%self.count)
  kind='network' if command[1]=='network' else 'container'
  name=command[-1] if kind=='network' else command[command.index('--name')+1]
  label=command[command.index('--label')+1].split('=',1)[1]
  self.resources[identifier]={'kind':kind,'name':name,'label':label}
  if interrupt: raise RuntimeError('TERM after daemon creation before ID capture')
  return (identifier+'\n').encode()
 def run(self,command,**kwargs):
  self.commands.append(command)
  if command[1:3] in (['rm','--force'],['network','rm']):
   assert command[-1]!='f'*64;self.resources.pop(command[-1]);return subprocess.CompletedProcess(command,0,b'',b'')
  if 'inspect' in command:
   identifier=command[-1];item=self.resources.get(identifier)
   if not item:return subprocess.CompletedProcess(command,1,b'',b'No such container')
   raw=f"{identifier} /{item['name']} {item['label']}\n".encode()
  else:
   label=next(x for x in command if x.startswith('label=')).split('=',2)[2];kind='container' if command[1]=='ps' else 'network'
   raw=''.join(i+'\n' for i,v in self.resources.items() if v['kind']==kind and v['label']==label).encode()
  return subprocess.CompletedProcess(command,0,raw,b'')
results={}
with tempfile.TemporaryDirectory() as tmp:
 p=Path(tmp);(p/'out').mkdir();daemon=Daemon();owned=d.OwnedDocker('actual-exercise-probe',run=daemon.run)
 def fake_command(evidence,stage,command,**kwargs):
  if stage=='create-container':daemon.create(command,interrupt=True)
  return subprocess.CompletedProcess(command,0,b'public probe\n',b'')
 def fake_build(args,scratch,evidence): q=scratch/'binaries';q.mkdir();return q
 evidence={'commands':[]};args=types.SimpleNamespace(product_root=p,trusted_root=p,output_dir=p/'out')
 with mock.patch.object(d.sys,'platform','linux'),mock.patch.object(d.shutil,'which',return_value='/public/tool'),mock.patch.object(d,'run_command',side_effect=fake_command),mock.patch.object(d,'build_product',side_effect=fake_build):
  try:d.exercise(args,evidence,owned)
  except RuntimeError as e: failure=str(e)
 assert 'TERM after daemon' in failure and evidence['exercise_failure']==failure
 assert set(daemon.resources)=={'f'*64} and evidence['cleanup']['verified'] and len(daemon.commands)>0
 results['actual_exercise_uncertain_create']={'preserved_failure':failure,'reader_cleanup':evidence['reader_cleanup'],'cleanup':evidence['cleanup'],'external_sentinel_unchanged':True,'queries_and_removals':len(daemon.commands)}
# Actual two-phase lifecycle, injecting both a successfully returned and unknown-outcome cleanup-helper creation.
for ambiguous in (False,True):
 daemon=Daemon();label='two-phase';owned=d.OwnedDocker(label,run=daemon.run)
 def command(name):return ['docker','create','--name',name,'--label','wk-stable-client-run='+label,'image']
 owned.create('container',command('wk-scl-two-phase-reader'),lambda stage,cmd:daemon.create(cmd))
 first=owned.cleanup(final=False);assert first['verified'] and owned.cleanup_receipt is None
 try:owned.create('container',command('wk-scl-two-phase-helper'),lambda stage,cmd:daemon.create(cmd,interrupt=ambiguous))
 except RuntimeError:assert ambiguous
 final=owned.cleanup();assert final['verified'] and set(daemon.resources)=={'f'*64}
 assert 'wk-scl-two-phase-helper' in final['creation_attempts']['containers']
 results['two_phase_helper_'+str(ambiguous)]={'first':first,'final':final,'external_sentinel_unchanged':True}
# File atime evidence is captured before the observer's own hashing; mere presence must fail.
with tempfile.TemporaryDirectory() as tmp:
 root=Path(tmp);path=root/'dnf-cache/repo/repodata/primary.xml';path.parent.mkdir(parents=True);path.write_bytes(b'authenticated metadata')
 before=d.cache_operation(root,'arm');after=d.cache_operation(root,'inventory')
 try:d.cache_evidence(before,after,require_reuse=True)
 except RuntimeError:pass
 else:raise AssertionError('mere presence accepted')
 before=d.cache_operation(root,'arm')
 subprocess.run(['python3','-c','from pathlib import Path;import sys;Path(sys.argv[1]).read_bytes()',str(path)],check=True)
 after=d.cache_operation(root,'inventory');proof=d.cache_evidence(before,after,require_reuse=True)
 assert proof['metadata_reads']==['dnf-cache/repo/repodata/primary.xml']
 sentinel=root.parent/(root.name+'-sentinel');sentinel.write_text('keep');(root/'escape').symlink_to(sentinel)
 try:
  removed=d.cache_operation(root,'remove');assert removed=={'remaining':[]} and sentinel.read_text()=='keep'
 finally:sentinel.unlink()
 results['cache_read_evidence']={'mere_presence_rejected':True,'separate_process_read_proved':True,'proof':proof,'cache_removed':removed,'outside_symlink_target_unchanged':True,'limitation':'Host filesystem probe, not Linux container ownership or native DNF execution.'}
(OUT/'closure-probe-results.json').write_text(json.dumps(results,indent=2)+'\n')
print(json.dumps({'passed':True,'cases':list(results),'native_invoked':False},indent=2))
