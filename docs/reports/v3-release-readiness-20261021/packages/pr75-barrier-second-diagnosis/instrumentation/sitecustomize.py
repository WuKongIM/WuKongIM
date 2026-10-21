"""TEST ONLY diagnostic observer; one bounded delay before actual native save."""
import os,json,time,hashlib,sys
from pathlib import Path
if os.environ.get('WK_DIAG_LOG_PATH'):
 import dnf,dnf.cli.cli,dnf.repo
 delayed=False
 def file_fact(path):
  p=Path(path)
  if not p.is_file():return None
  st=p.stat();result={'size':st.st_size,'mtime_ns':st.st_mtime_ns}
  if st.st_size<=131072:result['sha256']=hashlib.sha256(p.read_bytes()).hexdigest()
  return result
 def capture(base,event,repo=None,**extra):
  target=Path(os.environ['WK_DIAG_LOG_PATH']);assert not target.exists() or target.stat().st_size<131072
  state={'event':event,'now':time.time(),'argv':sys.argv,'global_cacheonly':base.conf.cacheonly,'global_cachedir':base.conf.cachedir,'persistdir':base.conf.persistdir,'check_config_file_age':base.conf.check_config_file_age,'repos':[]}
  pers=base._repo_persistor
  if pers is not None:
   state['persistor_path']=pers.db_path;path=Path(pers.db_path)
   if path.is_file() and path.stat().st_size<8192:state['persisted_expired']=json.loads(path.read_text())
  for r in ([repo] if repo is not None else list(base.repos.iter_enabled())):
   c=r._repo.getCachedir();row={'id':r.id,'metadata_expire':r.metadata_expire,'repo_gpgcheck':r.repo_gpgcheck,'gpgcheck':r.gpgcheck,'repofile':r.repofile,'cache':c,'repofile_fact':file_fact(r.repofile),'metadata_object_loaded':bool(r.metadata),'primary_files':{str(p):file_fact(p) for p in Path(c).rglob('primary.xml.gz')}}
   try:row['native_expired']=r._repo.isExpired()
   except Exception as e:row['native_expired_error']=str(e)
   state['repos'].append(row)
  state.update(extra)
  with target.open('a') as f:f.write(json.dumps(state,sort_keys=True)+'\n')
 original_configure=dnf.cli.cli.Cli._configure_repos
 def configure(self,*args,**kw):
  result=original_configure(self,*args,**kw);capture(self.base,'configured');return result
 dnf.cli.cli.Cli._configure_repos=configure
 original_store=dnf.Base._store_persistent_data
 def store(self,*args,**kw):
  global delayed
  capture(self,'before-store')
  if 'makecache' in sys.argv and not delayed:
   delay=float(os.environ.get('WK_DIAG_DELAY_STORE','0'));assert 0<=delay<=3
   time.sleep(delay);delayed=True
  capture(self,'before-native-store')
  result=original_store(self,*args,**kw);capture(self,'after-native-store');return result
 dnf.Base._store_persistent_data=store
