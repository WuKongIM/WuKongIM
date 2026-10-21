from pathlib import Path
import ast,copy,hashlib,importlib.util,json,os,subprocess,sys,tempfile
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-stable-native-clients')
OUT=Path(__file__).parent
sys.dont_write_bytecode=True
spec=importlib.util.spec_from_file_location('spec_drill',ROOT/'tests/native_stable_clients_drill.py');drill=importlib.util.module_from_spec(spec);spec.loader.exec_module(drill)
validator=drill.module('scripts/validate-production-package-clients.py')
sha=lambda b:hashlib.sha256(b).hexdigest()
result={'docker_executed':False,'native_acceptance_executed':False,'commands':[],'metadata':[],'read_proof':[]}
with tempfile.TemporaryDirectory(prefix='spec-cache-',dir=str(OUT)) as tmp:
 root=Path(tmp);cert=root/'public.asc';cert.write_bytes(b'public');boot=root/'bootstrap';boot.write_bytes(b'public')
 validator._host_ca_bundle=lambda:cert
 for family in ['apt','rpm']:
  cache=root/('cache-'+family)
  command=validator.stable_client_command('fixed-image',root,cert,family,boot)
  expected=[str(cache/name)+':'+dest+':rw' for name,dest in drill.cache_mounts(family)]
  for inject_exact in [False,True]:
   source=list(command)
   if inject_exact:
    source[2:2]=['--tmpfs',drill.cache_mounts(family)[-1][1]+':rw,size=1m','--tmpfs',drill.cache_mounts(family)[-1][1]+'-neighbor:rw,size=1m']
   modified=drill.cache_client_command(source,cache,family)
   retained=[];i=0
   while i<len(modified):
    if modified[i]=='--volume' and modified[i+1] in expected:i+=2
    else:retained.append(modified[i]);i+=1
   comparison=[];i=0
   while i<len(source):
    if source[i]=='--tmpfs' and source[i+1].split(':',1)[0] in [x[1] for x in drill.cache_mounts(family)]:i+=2
    else:comparison.append(source[i]);i+=1
   binds=[modified[i+1] for i,x in enumerate(modified[:-1]) if x=='--volume' and modified[i+1] in expected]
   assert binds==expected and len(set(x.split(':')[0] for x in binds))==len(binds)
   assert retained==comparison and modified[-1]==source[-1] and not cache.exists()
   assert '/tmp:rw,noexec,nosuid,nodev,size=256m' in modified
   result['commands'].append({'family':family,'exact_tmpfs_injected':inject_exact,'binds':binds,'whole_native_body_sha256':sha(source[-1].encode()),'remaining_tokens_identical':True,'parent_tmpfs_retained':True})
 for name,valid in [('dnf-cli-cache/repo/repodata/primary.xml.gz',True),('dnf-cache/repo.solv',True),('dnf-cli-cache/repo.solvx',True),('dnf-cache/expired_repos.json',False),('dnf-cli-cache/expired_repos.json',False),('dnf-state/repo/repodata/primary.xml.gz',False),('dnf-cli-state/repo.solv',False),('dnf-cli-cache/packages/pkg.rpm',False),('dnf-cli-cache/log.txt',False),('apt/lists/partial/Packages',False)]:
  item={'sha256':'a'*64,'size':1,'device':1,'inode':1,'atime_ns':1}
  try:drill.cache_evidence({}, {name:item},require_reuse=False);accepted=True
  except RuntimeError:accepted=False
  assert accepted==valid
  result['metadata'].append({'name':name,'accepted':accepted})
 for prefix in ['dnf-cache','dnf-cli-cache']:
  cache=root/('read-'+prefix);cache.mkdir();name=prefix+'/repo/repodata/primary.xml.gz';p=cache/name;p.parent.mkdir(parents=True);p.write_bytes(b'read evidence')
  before=drill.cache_inventory(cache,arm=True);after=drill.cache_inventory(cache)
  try:drill.cache_evidence(before,after,require_reuse=True);raise AssertionError('mere observation accepted')
  except RuntimeError:pass
  before=drill.cache_inventory(cache,arm=True);p.read_bytes();after=drill.cache_inventory(cache)
  proof=drill.cache_evidence(before,after,require_reuse=True);assert proof['metadata_reads']==[name]
  rejected=[]
  for field in ['device','inode','sha256','size','atime_ns','removed']:
   changed=copy.deepcopy(after)
   if field=='removed':changed.clear()
   elif field=='atime_ns':changed[name][field]=1
   elif field=='sha256':changed[name][field]='b'*64
   else:changed[name][field]+=1
   try:drill.cache_evidence(before,changed,require_reuse=True);raise AssertionError(field+' accepted')
   except RuntimeError:rejected.append(field)
  result['read_proof'].append({'root':prefix,'same_file_read_accepted':True,'mere_observation_rejected':True,'rejected_mutations':rejected})
 cache=root/'cleanup';cache.mkdir();sentinel=root/'sentinel';sentinel.write_bytes(b'keep')
 for child,_ in drill.cache_mounts('rpm'):
  p=cache/child;p.mkdir();(p/'file').write_bytes(b'owned')
 assert drill.cache_operation(cache,'remove')=={'remaining':[]} and sentinel.read_bytes()==b'keep'
 result['cleanup']={'four_owned_children_removed':True,'external_sentinel_preserved':True}
# Source boundaries compared independently with fixed HEAD; no executions.
source=(ROOT/'tests/native_stable_clients_drill.py').read_text();before=subprocess.check_output(['git','show','HEAD:tests/native_stable_clients_drill.py'],cwd=ROOT,text=True)
def blocks(text):
 return {n.name:ast.get_source_segment(text,n) for n in ast.parse(text).body if isinstance(n,(ast.FunctionDef,ast.ClassDef))}
a,b=blocks(before),blocks(source)
unchanged=['OwnedDocker','cache_inventory','cache_operation','cache_helper_command','run_command','verify_rpm_requests','verify_apt_by_hash_fault','build_product','seal_generations','materialize_routes']
result['unchanged_blocks']={n:sha(b[n].encode()) for n in unchanged if a[n]==b[n]}
assert len(result['unchanged_blocks'])==len(unchanged)
assert source[source.index('            for generation in (') :]==before[before.index('            for generation in ('):]
result['all_case_loops_and_cleanup_unchanged']=True
validator_path='scripts/validate-production-package-clients.py'
assert (ROOT/validator_path).read_bytes()==subprocess.check_output(['git','show','HEAD:'+validator_path],cwd=ROOT)
result['production_validator_unchanged_sha256']=sha((ROOT/validator_path).read_bytes())
(OUT/'probe-result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:v for k,v in result.items() if k not in ['commands','unchanged_blocks']},indent=2))
