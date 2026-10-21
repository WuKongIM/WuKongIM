import dnf, dnf.crypto, dnf.repo, dnf.yum.misc, inspect, json, subprocess
r={'dnf_version':dnf.__version__,'versions':subprocess.check_output(['rpm','-q','dnf','libdnf','python3-dnf','gnupg2','gpgme']).decode().splitlines(),'source':{}}
for name,value in [('dnf.crypto',dnf.crypto),('dnf.yum.misc',dnf.yum.misc)]:
 for symbol in dir(value):
  if any(s in symbol.lower() for s in ('key','gpg')):
   obj=getattr(value,symbol)
   if inspect.isfunction(obj):
    s=inspect.getsource(obj)
    if len(s)<18000:r['source'][name+'.'+symbol]=s
for name in ('_handle_load','load','_set_gpg_options'):
 if hasattr(dnf.repo.Repo,name):r['source']['Repo.'+name]=inspect.getsource(getattr(dnf.repo.Repo,name))
out=json.dumps(r,sort_keys=True,indent=2);assert len(out)<180000;print(out)
