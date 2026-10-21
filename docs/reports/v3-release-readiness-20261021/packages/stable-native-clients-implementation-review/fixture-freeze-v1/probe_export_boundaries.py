"""Control-flow/file-I/O probe only: all package/GPG operations are inert stubs."""
from pathlib import Path
from unittest import mock
import importlib.util, subprocess, tempfile, hashlib, json, os, ast
HERE=Path(__file__).resolve().parent
FILES=HERE/'files'

def load(name,path):
    spec=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
source=load('review_source',FILES/'tests/native_source_metadata_drill.py')
mod=load('review_candidate',FILES/'tests/native_stable_candidate_drill.py')
results=[]
for fault in ('none','B-verification','B-export-copy'):
 with tempfile.TemporaryDirectory(prefix='wk-fixture-review-') as temp:
  root=Path(temp);repo=root/'repo';(repo/'manifests').mkdir(parents=True);(repo/'keys').mkdir();trusted=root/'trusted';builder=trusted/'scripts/build-native-package-repositories.sh';builder.parent.mkdir(parents=True);builder.write_bytes(b'UNEXECUTED TEST STUB\n')
  (repo/'manifests/trusted-toolchain.json').write_text(json.dumps({'files':{'scripts/build-native-package-repositories.sh':hashlib.sha256(builder.read_bytes()).hexdigest()}}))
  output=root/'output';output.mkdir();binaries=root/'binaries';binaries.mkdir()
  for ver in ('3.0.0','3.0.1'):
   d=binaries/ver;d.mkdir()
   for name in ('wukongim','wkcli'):
    header=bytearray(64);header[:7]=b'\x7fELF\x02\x01\x01';header[16:20]=b'\x02\x00\x3e\x00';p=d/name;p.write_bytes(header+f'{ver}:{name}'.encode());p.chmod(0o755)
  state={'private':None,'events':[]};original_env=dict(os.environ)
  class Factory:
   def __init__(self,p):state['private']=p;p.mkdir();(p/'NOT-A-SECRET').write_text('inert cleanup marker')
   def close(self):state['events'].append('factory-close')
  class Keys:TestKeyFactory=Factory
  def fixtures(name,file):
   if file=='native_source_metadata_drill.py':return source
   if file=='test_validate_signing_material.py':return Keys
   raise AssertionError(file)
  def controls(scratch,factory,keys):
   c=scratch/'control';(c/'manifests').mkdir(parents=True);(c/'keys').mkdir()
   for name,value in [('bootstrap-packages.json',{'version':'1.1.0'}),('channels.json',{'channels':{'stable':{'enabled':False}}}),('stable-public-keys.json',{}),('signing-toolchain.json',{})]:(c/'manifests'/name).write_text(json.dumps(value))
   for fam in ('apt','rpm'):(c/'keys'/f'{fam}-stable.asc').write_text('INERT PUBLIC CERT '+fam)
   return c,{fam:(object(),'next') for fam in ('apt','rpm')}
  def fixture_source(scratch,*,version,binary_payloads):
   s=scratch/'source';(s/'assets').mkdir(parents=True);(s/'resolution.json').write_text(json.dumps({'version':version,'source_sha':None,'release_id':None}));return s
  def sign(scratch,family,*args):
   s=scratch/f'signed-{family}';s.mkdir();p=scratch/f'{family}-receipt.json';p.write_text('{}');return s,p
  def invoke(evidence,name,command,env,**kwargs):
   state['events'].append(name)
   def arg(flag):return Path(command[command.index(flag)+1])
   if name=='plan':return subprocess.CompletedProcess(command,0,b'{"schema":"wukongim.native_package_publication_plan/v2","control_sha":null}\n',b'')
   if name=='compose':
    c=arg('--output-root');(c/'site').mkdir(parents=True);(c/'audit').mkdir();(c/'site'/'data').write_text(c.parent.name);(c/'audit/snapshot.json').write_text('{"test_only":true}');return subprocess.CompletedProcess(command,0,b'',b'')
   if name in ('verify-after-secret-removal','archive-create','audit-create','audit-verify'):
    assert not state['private'].exists(),'private marker remains at verify/export boundary'
   if name=='verify-after-secret-removal' and arg('--site-root').parent.parent.name=='B' and fault=='B-verification':raise RuntimeError('injected B verification failure')
   if name=='archive-create':arg('--archive').write_bytes(b'INERT ARCHIVE')
   if name=='audit-create':arg('--output').write_text('{}')
   return subprocess.CompletedProcess(command,0,b'',b'')
  real_copy=mod.shutil.copytree
  def copy(src,dst,*args,**kwargs):
   if Path(dst)==output/'B' and fault=='B-export-copy':
    Path(dst).mkdir();(Path(dst)/'partial').write_text('injected partial');raise OSError('injected copy failure')
   return real_copy(src,dst,*args,**kwargs)
  failure=None;result=None
  with mock.patch.object(mod,'ROOT',repo),mock.patch.object(mod,'load_fixture',side_effect=fixtures),mock.patch.object(mod,'generate_control',side_effect=controls),mock.patch.object(mod,'fixture_source',side_effect=fixture_source),mock.patch.object(mod,'sign_family',side_effect=sign),mock.patch.object(mod,'invoke',side_effect=invoke),mock.patch.object(mod.sys,'platform','linux'),mock.patch.object(mod.os,'geteuid',return_value=65532),mock.patch.object(mod.shutil,'which',return_value='/test/tool'),mock.patch.object(mod.shutil,'copytree',side_effect=copy),mock.patch.object(mod.subprocess,'run',side_effect=AssertionError('native command forbidden')):
   try:result=mod.export_client_generations(output_root=output,binaries_root=binaries,trusted_root=trusted,product_source_sha='fc18221282f289088ec9b4282fda8e0bfc67a3b8')
   except (OSError,RuntimeError) as e:failure=str(e)
  assert dict(os.environ)==original_env
  assert state['private'] and not state['private'].exists()
  if fault=='none':
   assert result['secret_tree_removed'] and set(result['generations'])=={'A','B'}
   assert result['generations']['A']['version']=='3.0.0' and result['generations']['B']['version']=='3.0.1'
   assert result['generations']['A']['bootstrap_version']=='1.1.1' and result['generations']['B']['bootstrap_version']=='1.1.2'
   for fam in ('apt','rpm'):assert (output/'A/control/keys'/f'{fam}-stable.asc').read_bytes()==(output/'B/control/keys'/f'{fam}-stable.asc').read_bytes()
   assert not failure
  else:assert failure is not None and failure.startswith('injected ') and list(output.iterdir())==[]
  results.append({'case':fault,'passed':True,'private_removed':True,'output_generations':sorted(x.name for x in output.iterdir()),'failure':failure,'environment_restored':True})
print(json.dumps({'native_commands_executed':False,'cryptographic_or_package_validation_claimed':False,'results':results},indent=2))
