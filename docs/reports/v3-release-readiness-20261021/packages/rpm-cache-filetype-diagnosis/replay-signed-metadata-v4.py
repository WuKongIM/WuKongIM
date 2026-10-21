import dnf,dnf.cli.cli,dnf.repo,gzip,hashlib,importlib.util,json,os,re,shutil,signal,stat,subprocess
from pathlib import Path
from xml.etree import ElementTree as ET

def run(command):
 r=subprocess.run(command,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=15)
 assert len(r.stdout)+len(r.stderr)<65536
 if r.returncode:raise RuntimeError('fixture command failed '+command[0]+': '+r.stderr.decode(errors='replace')[:1000])
 return r.stdout
secret=Path('/tmp/cache-filetype-test-secret');secret.mkdir(mode=0o700)
repo_root=Path('/tmp/cache-filetype-repo');metadata=repo_root/'repodata';metadata.mkdir(parents=True)
fixture={'test_only':True,'key_purpose':'new ephemeral RSA2048 signing key for empty metadata only; not production key policy','network':'none','repo_gpgcheck':True,'new_test_secret_removed_before_native_load':False}
try:
 run(['gpg','--homedir',str(secret),'--batch','--pinentry-mode','loopback','--passphrase','','--quick-generate-key','CACHE FILETYPE TEST ONLY <cache-filetype@test.invalid>','rsa2048','sign','1d'])
 cert=run(['gpg','--homedir',str(secret),'--batch','--armor','--export'])
 assert cert.startswith(b'-----BEGIN PGP PUBLIC KEY BLOCK-----')
 (repo_root/'public.asc').write_bytes(cert)
 ns='http://linux.duke.edu/metadata/repo';ET.register_namespace('',ns);root=ET.Element('{'+ns+'}repomd');ET.SubElement(root,'{'+ns+'}revision').text='1'
 files={
 'primary':b'<?xml version="1.0" encoding="UTF-8"?><metadata xmlns="http://linux.duke.edu/metadata/common" xmlns:rpm="http://linux.duke.edu/metadata/rpm" packages="0"></metadata>\n',
 'filelists':b'<?xml version="1.0" encoding="UTF-8"?><filelists xmlns="http://linux.duke.edu/metadata/filelists" packages="0"></filelists>\n',
 'other':b'<?xml version="1.0" encoding="UTF-8"?><otherdata xmlns="http://linux.duke.edu/metadata/other" packages="0"></otherdata>\n'}
 for name,plain in sorted(files.items()):
  compressed=gzip.compress(plain,mtime=0);filename=name+'.xml.gz';(metadata/filename).write_bytes(compressed)
  item=ET.SubElement(root,'{'+ns+'}data',{'type':name})
  ET.SubElement(item,'{'+ns+'}checksum',{'type':'sha256'}).text=hashlib.sha256(compressed).hexdigest()
  ET.SubElement(item,'{'+ns+'}open-checksum',{'type':'sha256'}).text=hashlib.sha256(plain).hexdigest()
  ET.SubElement(item,'{'+ns+'}location',{'href':'repodata/'+filename})
  ET.SubElement(item,'{'+ns+'}timestamp').text='1700000000'
  ET.SubElement(item,'{'+ns+'}size').text=str(len(compressed));ET.SubElement(item,'{'+ns+'}open-size').text=str(len(plain))
 (metadata/'repomd.xml').write_bytes(ET.tostring(root,encoding='utf-8',xml_declaration=True))
 run(['gpg','--homedir',str(secret),'--batch','--pinentry-mode','loopback','--passphrase','','--armor','--digest-algo','SHA256','--detach-sign','--output',str(metadata/'repomd.xml.asc'),str(metadata/'repomd.xml')])
 fixture['public_files']={str(p.relative_to(repo_root)):{'size':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} for p in repo_root.rglob('*') if p.is_file()}
finally:
 run(['gpgconf','--homedir',str(secret),'--kill','gpg-agent']);shutil.rmtree(secret)
 fixture['new_test_secret_removed_before_native_load']=not secret.exists()
assert fixture['new_test_secret_removed_before_native_load']
repo_dir=Path('/tmp/repos.d');repo_dir.mkdir()
(repo_dir/'wukongim-stable.repo').write_text('''[wukongim-stable]
name=WuKongIM TEST ONLY metadata cache probe
baseurl=file:///tmp/cache-filetype-repo
enabled=1
includepkgs=wukongim,wukongim-release-stable
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///tmp/cache-filetype-repo/public.asc
sslverify=1
metadata_expire=0
skip_if_unavailable=0
''')
args=['--quiet','--assumeyes','--installroot=/tmp/client-root','--releasever=9','--disablerepo=*','--enablerepo=wukongim-stable','--setopt=reposdir=/tmp/repos.d','--setopt=cachedir=/tmp/dnf-cache','--setopt=persistdir=/tmp/dnf-state','--setopt=keepcache=False','--setopt=install_weak_deps=False','makecache','--refresh']
base=dnf.cli.cli.BaseCli();cli=dnf.cli.cli.Cli(base);cli.configure(args)
repo=base.repos['wukongim-stable'];cache=Path('/tmp/dnf-cache')
spec=importlib.util.spec_from_file_location('shipping_cache_probe','/probe/shipping-driver.py');ship=importlib.util.module_from_spec(spec);spec.loader.exec_module(ship)
def snapshot():
 values=[]
 if cache.exists():
  for p in sorted(cache.rglob('*')):
   s=p.lstat();typ=('regular' if stat.S_ISREG(s.st_mode) else 'directory' if stat.S_ISDIR(s.st_mode) else 'socket' if stat.S_ISSOCK(s.st_mode) else 'symlink' if stat.S_ISLNK(s.st_mode) else 'fifo' if stat.S_ISFIFO(s.st_mode) else 'other')
   values.append({'path':str(p.relative_to(cache)),'type':typ,'mode':oct(s.st_mode),'nlink':s.st_nlink,'size':s.st_size,'regular':stat.S_ISREG(s.st_mode),'nlink_is_one':s.st_nlink==1,'within_64MiB':s.st_size<=64*1024*1024})
 assert len(values)<128
 try:inventory={'accepted':True,'entries':ship.cache_inventory(cache)}
 except RuntimeError as error:inventory={'accepted':False,'error':str(error)}
 return {'stat':values,'shipping_inventory':inventory}
r={'test_only':True,'native_package_download':False,'repo_transport_difference':'file:// local empty signed repository; same pinned native Repo.load metadata/key verification path, no original CI stat recovery','fixture':fixture,'args':args,'versions':run(['rpm','-q','dnf','libdnf','gnupg2','gpgme']).decode().splitlines(),'repo':{'id':repo.id,'native_cachedir':repo._repo.getCachedir(),'pubring_dir':repo._pubring_dir,'repo_gpgcheck':repo.repo_gpgcheck},'phases':{}}
r['phases']['before_load']=snapshot()
try:
 r['native_repo_load_return']=repo.load();r['native_repo_load_succeeded']=True
 r['phases']['after_load']=snapshot()
finally:base.close()
r['phases']['after_close']=snapshot()
# Emulate the owned import agent's abrupt exit when a client container exits;
# retain the special inodes for a subsequent cache inventory, as on a bind mount.
try:
 data=run(['gpg-connect-agent','--homedir',repo._pubring_dir,'--no-autostart','GETINFO pid','/bye']).decode()
 pid=int(re.search(r'^D ([0-9]+)$',data,re.M).group(1))
 cmdline=Path('/proc/'+str(pid)+'/cmdline').read_bytes().split(b'\0')
 r['owned_import_agent_observed']={'pid':pid,'cmdline':[x.decode() for x in cmdline if x]}
 executable=(cmdline[0]==b'/usr/bin/gpg-agent' or cmdline[:2]==[b'/run/rosetta/rosetta',b'/usr/bin/gpg-agent'])
 assert executable and b'--homedir' in cmdline and cmdline[cmdline.index(b'--homedir')+1]==repo._pubring_dir.encode()
 os.kill(pid,signal.SIGKILL)
 r['owned_import_agent_sigkill']={'pid':pid,'cmdline':[x.decode() for x in cmdline if x]}
 import time
 for _ in range(100):
  state=Path('/proc/'+str(pid)+'/status')
  if not state.exists():
   r['owned_import_agent_sigkill']['terminal_state']='absent';break
  text=state.read_text()
  if re.search(r'^State:\s+Z',text,re.M):
   r['owned_import_agent_sigkill']['terminal_state']='zombie';break
  time.sleep(.01)
 else:raise RuntimeError('owned agent did not stop within 1 second')
except (RuntimeError,FileNotFoundError,AttributeError,AssertionError) as error:r['owned_import_agent_sigkill']={'not_performed':str(error)}
r['phases']['after_agent_exit']=snapshot()
# Diagnostic-only single-variable difference: remove only observed socket inodes,
# never call a modified shipping inventory and never remove regular cache content.
removed=[]
for p in sorted(cache.rglob('*')):
 if stat.S_ISSOCK(p.lstat().st_mode):
  removed.append(str(p.relative_to(cache)));p.unlink()
r['diagnostic_only_removed_sockets']=removed
r['phases']['after_socket_removal']=snapshot()
print(json.dumps(r,sort_keys=True,indent=2),flush=True)
assert r['native_repo_load_succeeded'] and not r['phases']['after_load']['shipping_inventory']['accepted'], 'native signed load did not reproduce rejection'
assert r['phases']['after_socket_removal']['shipping_inventory']['accepted'], 'socket-only removal did not isolate the rejection'
assert r['owned_import_agent_sigkill'].get('terminal_state') in ('absent','zombie'), 'owned import agent termination was not observed'
