import dnf, dnf.cli.cli, dnf.crypto, dnf.repo, inspect, json, subprocess, stat, importlib.util
from pathlib import Path
repo_dir=Path('/tmp/repos.d');repo_dir.mkdir()
(repo_dir/'wukongim-stable.repo').write_text('''[wukongim-stable]
name=WuKongIM TEST ONLY public-key cache probe
mirrorlist=https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist
enabled=1
includepkgs=wukongim,wukongim-release-stable
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///probe/public.asc
sslverify=1
metadata_expire=0
skip_if_unavailable=0
''')
args=['--quiet','--assumeyes','--installroot=/tmp/client-root','--releasever=9','--disablerepo=*','--enablerepo=wukongim-stable','--setopt=reposdir=/tmp/repos.d','--setopt=cachedir=/tmp/dnf-cache','--setopt=persistdir=/tmp/dnf-state','--setopt=keepcache=False','--setopt=install_weak_deps=False','makecache','--refresh']
base=dnf.cli.cli.BaseCli();cli=dnf.cli.cli.Cli(base);cli.configure(args)
repo=base.repos['wukongim-stable'];cache=Path('/tmp/dnf-cache')
spec=importlib.util.spec_from_file_location('shipping_cache_probe','/probe/shipping-driver.py')
ship=importlib.util.module_from_spec(spec);spec.loader.exec_module(ship)
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
r={'test_only':True,'native_metadata_or_payload_download':False,'public_key_only':True,'args':args,'versions':subprocess.check_output(['rpm','-q','dnf','libdnf','gnupg2','gpgme']).decode().splitlines(),'repo':{'id':repo.id,'native_cachedir':repo._repo.getCachedir(),'pubring_dir':repo._pubring_dir,'repo_gpgcheck':repo.repo_gpgcheck},'phases':{}}
r['phases']['before_import']=snapshot()
dnf.crypto.import_repo_keys(repo)
r['phases']['after_import']=snapshot()
r['imported_keyids']=dnf.crypto.keyids_from_pubring(repo._pubring_dir)
base.close()
r['phases']['after_close']=snapshot()
out=json.dumps(r,sort_keys=True,indent=2);assert len(out)<128000;print(out,flush=True)
assert not r['phases']['after_import']['shipping_inventory']['accepted'], 'public key import did not reproduce inventory rejection'
