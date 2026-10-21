import dnf,dnf.cli.cli,dnf.conf.config,dnf.repo,inspect,json,subprocess
from pathlib import Path
repo=Path('/tmp/repos.d');repo.mkdir(parents=True)
(repo/'wukongim-stable.repo').write_text("""[wukongim-stable]
name=WuKongIM stable
mirrorlist=https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist
enabled=1
includepkgs=wukongim,wukongim-release-stable
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-stable
sslverify=1
metadata_expire=0
skip_if_unavailable=0
""")
args=['--quiet','--assumeyes','--installroot=/tmp/client-root','--releasever=9','--disablerepo=*','--enablerepo=wukongim-stable','--setopt=reposdir=/tmp/repos.d','--setopt=cachedir=/tmp/dnf-cache','--setopt=persistdir=/tmp/dnf-state','--setopt=keepcache=False','--setopt=install_weak_deps=False','makecache','--refresh']
base=dnf.cli.cli.BaseCli();cli=dnf.cli.cli.Cli(base);cli.configure(args)
result={'dnf_version':dnf.__version__,'rpm_versions':subprocess.check_output(['rpm','-q','dnf','libdnf','python3-dnf']).decode().splitlines(),'args':args,'main':{key:getattr(base.conf,key) for key in ['cachedir','persistdir','installroot','keepcache']},'repo':[],'source':{}}
for r in base.repos.values():
 result['repo'].append({'id':r.id,'native_cachedir':r._repo.getCachedir(),'attributes':[key for key in dir(r._repo) if 'dir' in key.lower()]})
for name,method in [('prepend_installroot',dnf.conf.config.MainConf.prepend_installroot),('repo_init',dnf.repo.Repo.__init__),('configure_repos',dnf.cli.cli.Cli._configure_repos)]:
 result['source'][name]=inspect.getsource(method)
print(json.dumps(result,sort_keys=True,indent=2),flush=True)
import os;os._exit(0)
