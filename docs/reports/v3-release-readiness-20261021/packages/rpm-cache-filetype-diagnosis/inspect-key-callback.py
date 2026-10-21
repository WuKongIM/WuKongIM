import dnf.repo,dnf.crypto,inspect,json
r={'repo_native_methods':[n for n in dir(__import__('libdnf').repo.Repo) if any(v in n.lower() for v in ('gpg','key','import'))],'source':{}}
for n in dir(dnf.repo):
 obj=getattr(dnf.repo,n)
 if inspect.isclass(obj) and any(s in n.lower() for s in ('key','repo')):
  if n in ('Repo','Metadata'):continue
  try:r['source'][n]=inspect.getsource(obj)
  except (OSError,TypeError):pass
for n in ('_pubring_dir','_init_libdnf_repo','__init__'):
 obj=getattr(dnf.repo.Repo,n,None)
 if isinstance(obj,property):obj=obj.fget
 if obj:r['source']['Repo.'+n]=inspect.getsource(obj)
out=json.dumps(r,indent=2,sort_keys=True);assert len(out)<100000;print(out)
