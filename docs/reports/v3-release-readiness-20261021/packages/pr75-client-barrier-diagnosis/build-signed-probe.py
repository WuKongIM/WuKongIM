"""TEST ONLY data-only signed repositories; secrets stay in container tmpfs."""
from pathlib import Path
import importlib.util,sys,subprocess,hashlib,json,shutil,tempfile,os
s=importlib.util.spec_from_file_location('tiny','/control/tests/native_source_metadata_drill.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
def run(a):
 r=subprocess.run(a,capture_output=True,timeout=90);assert r.returncode==0,(a,r.stderr.decode());return r.stdout
with tempfile.TemporaryDirectory(prefix='/tmp/signed-probe-') as t:
 root=Path(t);home=root/'gnupg';home.mkdir(mode=0o700)
 try:
  run(['gpg','--homedir',str(home),'--batch','--pinentry-mode','loopback','--passphrase','','--quick-generate-key','BARRIER TEST ONLY <test@invalid>','rsa2048','sign','1d'])
  listing=run(['gpg','--homedir',str(home),'--batch','--with-colons','--list-keys']).decode();fpr=next(l.split(':')[9] for l in listing.splitlines() if l.startswith('fpr:'))
  public=root/'public';public.mkdir();(public/'rpm-test.asc').write_bytes(run(['gpg','--homedir',str(home),'--armor','--export',fpr]))
  facts={}
  for gen,version in [('A','3.0.0'),('B','3.0.1')]:
   build=root/gen;build.mkdir();_,rpm=m.build_packages(build,version,version)
   repo=public/gen;(repo/'Packages').mkdir(parents=True)
   dest=repo/'Packages'/('wukongim_'+version+'_linux_amd64.rpm');shutil.copyfile(rpm,dest)
   run(['rpmsign','--define','_gpg_name '+fpr,'--define','_gpg_path '+str(home),'--define','__gpg /usr/bin/gpg','--define','_gpg_digest_algo sha256','--addsign',str(dest)])
   run(['createrepo_c','--no-database','--simple-md-filenames','--checksum','sha256',str(repo)])
   run(['gpg','--homedir',str(home),'--batch','--armor','--detach-sign',str(repo/'repodata/repomd.xml')])
   facts[gen]={'version':version,'member':dest.relative_to(public).as_posix(),'sha256':hashlib.sha256(dest.read_bytes()).hexdigest(),'size':dest.stat().st_size}
  # Retained alias: metadata stays B, but B's payload prefix can resolve exact A bytes.
  shutil.copyfile(public/facts['A']['member'],public/'B'/'Packages'/Path(facts['A']['member']).name)
  (public/'facts.json').write_text(json.dumps({'test_only':True,'fingerprint':fpr,'payloads':facts},indent=2)+'\n')
 finally:
  run(['gpgconf','--homedir',str(home),'--kill','all'])
  shutil.rmtree(home)
 # Only public cert, signed metadata and signed data-only RPMs leave scratch.
 shutil.copytree(public,'/output/public',dirs_exist_ok=False)
 print(json.dumps({'exported_public_files':sorted(p.relative_to(public).as_posix() for p in public.rglob('*') if p.is_file()),'secrets_destroyed_before_export':not home.exists()}))
