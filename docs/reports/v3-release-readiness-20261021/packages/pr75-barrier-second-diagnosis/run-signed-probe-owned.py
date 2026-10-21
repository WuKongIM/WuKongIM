from pathlib import Path
import subprocess,uuid,json,re,sys,os
root=Path(__file__).parent
control=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-barrier-second-diagnosis')
label='second-barrier-'+uuid.uuid4().hex;name='wk-'+label;steps=[];ok=False
kind=sys.argv[1]
image=('ghcr.io/wukongim/native-package-signing-toolchain@sha256:769d49a4882439a35fdf4497a4b97f09843a9dc92f4074447d48e43b2614d922' if kind=='build' else 'quay.io/rockylinux/rockylinux@sha256:aa1303868c5fad2c780735de874d9dafefe6743b701579eb7acd9bd8bdc523b5')
def run(args,timeout=30):
 r=subprocess.run(args,capture_output=True,timeout=timeout);assert len(r.stdout)+len(r.stderr)<2*1024*1024
 steps.append({'command':args,'exit_code':r.returncode,'stdout':r.stdout.decode(errors='replace'),'stderr':r.stderr.decode(errors='replace')});return r
try:
 c=['docker','create','--platform','linux/amd64','--name',name,'--label','wk-stable-barrier-diagnosis='+label,'--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--cpus','2','--memory','768m','--pids-limit','128','--tmpfs','/tmp:rw,nosuid,nodev,size=384m','--tmpfs','/var/log:rw,nosuid,nodev,size=32m','--volume',str(root)+':/probe:ro','--volume',str(control)+':/control:ro']
 if kind=='build':
  out=root/'signed-public';out.mkdir();c+=['--user',str(os.getuid())+':'+str(os.getgid()),'--volume',str(out)+':/output:rw'];script='/probe/build-signed-probe.py'
 else:
  c+=['--volume',str(root/'signed-public/public/rpm-test.asc')+':/keys/rpm-stable.asc:ro','--user','0:0','--add-host','packages.githubim.com:127.0.0.1','--tmpfs','/downloads:rw,nosuid,nodev,size=64m','--tmpfs','/etc/pki/rpm-gpg:rw,nosuid,nodev,size=8m','--tmpfs','/etc/yum.repos.d:rw,nosuid,nodev,size=8m','--tmpfs','/var/cache/dnf:rw,nosuid,nodev,size=64m'];script='/probe/signed-barrier-probe.py'
 c+=['--entrypoint','python3',image,'-B',script]
 assert run(c).returncode==0
 r=run(['docker','start','--attach',name],300)
 (root/('signed-'+kind+'.stdout')).write_bytes(r.stdout);(root/('signed-'+kind+'.stderr')).write_bytes(r.stderr)
 ok=r.returncode==0
 print(json.dumps({'kind':kind,'exit':r.returncode,'stdout_size':len(r.stdout),'stderr':r.stderr.decode()[-2000:]}))
finally:
 inv=run(['docker','ps','-aq','--no-trunc','--filter','label=wk-stable-barrier-diagnosis='+label]);assert inv.returncode==0
 for identifier in inv.stdout.decode().split():
  assert re.fullmatch('[0-9a-f]{64}',identifier)
  fact=run(['docker','inspect','--format','{{.Name}} {{index .Config.Labels "wk-stable-barrier-diagnosis"}}',identifier]);assert fact.stdout.decode().strip()=='/'+name+' '+label
  assert run(['docker','rm','--force',identifier]).returncode==0
 remaining=run(['docker','ps','-aq','--filter','label=wk-stable-barrier-diagnosis='+label]);assert remaining.returncode==0 and not remaining.stdout.strip()
 (root/('signed-'+kind+'.receipt.json')).write_text(json.dumps({'label':label,'cleanup_verified':True,'probe_passed':ok,'steps':steps},indent=2)+'\n')
if not ok:sys.exit(1)
