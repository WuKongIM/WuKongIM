from pathlib import Path
import subprocess,uuid,json,re,sys,time
root=Path(__file__).parent
label='barrier-diagnosis-'+uuid.uuid4().hex
name='wk-'+label
image='quay.io/rockylinux/rockylinux@sha256:aa1303868c5fad2c780735de874d9dafefe6743b701579eb7acd9bd8bdc523b5'
steps=[]
def run(args,timeout=45):
 r=subprocess.run(args,capture_output=True,timeout=timeout);steps.append({'command':args,'exit_code':r.returncode,'stdout':r.stdout.decode(errors='replace'),'stderr':r.stderr.decode(errors='replace')});return r
try:
 c=['docker','create','--platform','linux/amd64','--name',name,'--label','wk-stable-barrier-diagnosis='+label,'--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','0:0','--cpus','1','--memory','512m','--pids-limit','64','--tmpfs','/tmp:rw,nosuid,nodev,size=128m','--volume',str(root)+':/probe:ro','--entrypoint','python3',image,'-B','/probe/'+sys.argv[1]]
 r=run(c);assert r.returncode==0
 r=run(['docker','start','--attach',name]);(root/(sys.argv[1]+'.stdout')).write_bytes(r.stdout);(root/(sys.argv[1]+'.stderr')).write_bytes(r.stderr)
 print('probe exit',r.returncode,'stdout',len(r.stdout),'stderr',r.stderr.decode()[:1000])
finally:
 inv=run(['docker','ps','-aq','--filter','label=wk-stable-barrier-diagnosis='+label]);assert inv.returncode==0
 for identifier in inv.stdout.decode().split():
  assert re.fullmatch('[0-9a-f]{12,64}',identifier)
  fact=run(['docker','inspect','--format','{{.Name}} {{index .Config.Labels "wk-stable-barrier-diagnosis"}}',identifier]);assert fact.stdout.decode().strip()=='/'+name+' '+label
  assert run(['docker','rm','--force',identifier]).returncode==0
 remaining=run(['docker','ps','-aq','--filter','label=wk-stable-barrier-diagnosis='+label]);assert remaining.returncode==0 and not remaining.stdout.strip()
 (root/(sys.argv[1]+'.receipt.json')).write_text(json.dumps({'label':label,'cleanup_verified':True,'steps':steps},indent=2)+'\n')
