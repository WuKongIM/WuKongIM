"""TEST ONLY offline native libdnf freshness boundary, not signed download proof."""
import gzip,hashlib,json,os,time,subprocess,inspect
from pathlib import Path
import dnf, dnf.repo
base=dnf.Base();base.conf.cachedir='/tmp/probe-cache';base.conf.persistdir='/tmp/probe-state';base.conf.check_config_file_age=False
repo=dnf.repo.Repo('wukongim-stable',base.conf)
repo.mirrorlist='https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist'
repo.metadata_expire=0
# No keys or signed assets are used: this isolated probe only tests age arithmetic.
repo.repo_gpgcheck=False;repo.gpgcheck=False
root=Path(repo._repo.getCachedir());(root/'repodata').mkdir(parents=True)
xml=b'<?xml version="1.0" encoding="UTF-8"?><metadata xmlns="http://linux.duke.edu/metadata/common" xmlns:rpm="http://linux.duke.edu/metadata/rpm" packages="0"></metadata>'
data=gzip.compress(xml,mtime=0);primary=root/'repodata/primary.xml.gz';primary.write_bytes(data)
repomd=('<?xml version="1.0"?><repomd xmlns="http://linux.duke.edu/metadata/repo"><revision>1</revision><data type="primary"><checksum type="sha256">'+hashlib.sha256(data).hexdigest()+'</checksum><open-checksum type="sha256">'+hashlib.sha256(xml).hexdigest()+'</open-checksum><location href="repodata/primary.xml.gz"/><timestamp>1</timestamp><size>'+str(len(data))+'</size><open-size>'+str(len(xml))+'</open-size></data></repomd>').encode()
(root/'repodata/repomd.xml').write_bytes(repomd)
assert repo._repo.loadCache(True)
observed=[]
for age in [0,3]:
 now=int(time.time());os.utime(primary,(now-age,now-age))
 result={'requested_age':age,'now':int(time.time()),'primary_mtime':int(primary.stat().st_mtime),'native_age':repo._repo.getAge(),'native_expired':repo._repo.isExpired()}
 observed.append(result)
assert observed[0]['native_age']==0 and observed[0]['native_expired'] is False
assert observed[1]['native_age']>=3 and observed[1]['native_expired'] is True
repo._repo.setSyncStrategy(dnf.repo.SYNC_ONLY_CACHE)
loaded=repo.load()
assert loaded is False and base.conf.cacheonly is False
print(json.dumps({'rpm_versions':subprocess.check_output(['rpm','-q','dnf','libdnf','python3-dnf'],text=True).splitlines(),'metadata_expire':repo.metadata_expire,'repo_cache':str(root),'observed':observed,'only_repo_cache':{'load_downloaded':loaded,'global_cacheonly':base.conf.cacheonly,'strategy':repo._repo.getSyncStrategy()},'global_cacheonly_package_rule':inspect.getsource(dnf.Base._select_remote_pkgs),'limits':'Unsigned local metadata, no network; proves actual pinned library freshness and repository-only cache API, not GPG or retained-payload download.'},indent=2))
base.close()
