from pathlib import Path
import datetime, hashlib, json, re, subprocess, sys, time, uuid
root=Path(__file__).parent
images={
 'rocky':'quay.io/rockylinux/rockylinux:9-ubi-init@sha256:aa1303868c5fad2c780735de874d9dafefe6743b701579eb7acd9bd8bdc523b5',
 'alma':'almalinux:9@sha256:28db580abb508f7ccbc0ac6d53e1d8da9d42a26c77fa3dcc26ac2726673fbe3e'}
assert len(sys.argv)==3 and sys.argv[1] in images
script=sys.argv[2];assert re.fullmatch(r'[a-z0-9-]+\.py',script) and (root/script).is_file()
image=images[sys.argv[1]];run_id=script[:-3]+'-'+sys.argv[1]
assert not (root/(run_id+'.receipt.json')).exists()
label='filetype-'+uuid.uuid4().hex;name='wk-'+label;steps=[]
receipt={'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'label':label,'name':name,'distribution':sys.argv[1],'image':image,'probe':script,'probe_sha256':hashlib.sha256((root/script).read_bytes()).hexdigest(),'cleanup_verified':False,'steps':steps}
def run(args,timeout=45):
 start=time.monotonic()
 try:
  r=subprocess.run(args,capture_output=True,timeout=timeout)
  assert len(r.stdout)+len(r.stderr)<=1024*1024,'command output exceeded 1 MiB'
 except subprocess.TimeoutExpired as e:
  steps.append({'command':args,'timed_out':True,'elapsed_seconds':time.monotonic()-start});raise
 steps.append({'command':args,'exit_code':r.returncode,'elapsed_seconds':time.monotonic()-start,'stdout':r.stdout.decode(errors='replace'),'stderr':r.stderr.decode(errors='replace')});return r
try:
 r=run(['docker','image','inspect',image]);assert r.returncode==0,'required pinned local image unavailable'
 values=json.loads(r.stdout);assert len(values)==1
 fact=values[0];assert fact['Architecture']=='amd64' and fact['Os']=='linux'
 receipt['image_identity']={k:fact[k] for k in ('Id','RepoDigests','Architecture','Os')}
 # Do not retain complete generic inspect metadata; image identity is sufficient.
 steps[-1]['stdout']='[image identity recorded above]'
 cmd=['docker','create','--pull=never','--platform','linux/amd64','--name',name,'--label','wk-stable-cache-filetype='+label,'--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','0:0','--cpus','1','--memory','512m','--pids-limit','64','--tmpfs','/tmp:rw,nosuid,nodev,size=128m','--volume',str(root)+':/probe:ro','--entrypoint','python3',image,'-B','/probe/'+script]
 r=run(cmd);assert r.returncode==0
 receipt['container_id']=r.stdout.decode().strip();assert re.fullmatch('[0-9a-f]{64}',receipt['container_id'])
 r=run(['docker','start','--attach',receipt['container_id']])
 receipt['probe_exit_code']=r.returncode
 (root/(run_id+'.stdout')).write_bytes(r.stdout);(root/(run_id+'.stderr')).write_bytes(r.stderr)
 receipt['stdout_sha256']=hashlib.sha256(r.stdout).hexdigest();receipt['stderr_sha256']=hashlib.sha256(r.stderr).hexdigest()
 print(json.dumps({'run_id':run_id,'exit':r.returncode,'stdout_bytes':len(r.stdout),'stderr':r.stderr.decode(errors='replace')[:2000]}))
finally:
 try:
  inv=run(['docker','ps','-aq','--filter','label=wk-stable-cache-filetype='+label]);assert inv.returncode==0
  for identifier in inv.stdout.decode().split():
   assert re.fullmatch('[0-9a-f]{12,64}',identifier)
   fact=run(['docker','inspect','--format','{{.Name}} {{index .Config.Labels "wk-stable-cache-filetype"}}',identifier]);assert fact.returncode==0 and fact.stdout.decode().strip()=='/'+name+' '+label
   assert run(['docker','rm','--force',identifier]).returncode==0
  remaining=run(['docker','ps','-aq','--filter','label=wk-stable-cache-filetype='+label]);assert remaining.returncode==0 and not remaining.stdout.strip()
  receipt['cleanup_verified']=True;receipt['remaining_container_ids']=[]
 finally:
  receipt['finished_at']=datetime.datetime.now(datetime.timezone.utc).isoformat()
  (root/(run_id+'.receipt.json')).write_text(json.dumps(receipt,indent=2)+'\n')
