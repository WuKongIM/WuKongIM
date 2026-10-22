#!/usr/bin/env python3
import pathlib,hashlib,subprocess,os,json,zipfile,tarfile,struct,datetime
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/local-binary-provenance-audit-v1');C=R.parent
GO=pathlib.Path('/Users/tt/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.11.darwin-arm64/bin/go')
Z=pathlib.Path('/tmp/wukongim-v3-readiness-20261021/cloud/pr1024-authorized-run-resume-20261023-private/live-observation/bundle-12445080855.zip')
PUB=pathlib.Path('/tmp/wukongim-v3-readiness-20261021/cloud/pr1024-authorized-run-resume-20261023/live-observation/bundle')
sha=lambda b:hashlib.sha256(b).hexdigest()
def hashfile(p):
 h=hashlib.sha256();size=0
 with p.open('rb') as f:
  for b in iter(lambda:f.read(1<<20),b''):h.update(b);size+=len(b)
 return h.hexdigest(),size
inputs={}
def bind(p):
 p=pathlib.Path(p);h,n=hashfile(p);inputs[str(p)]={'sha256':h,'bytes':n};return h,n
def metainfo(p,name):
 env=dict(os.environ,GOTOOLCHAIN='local',GOWORK='off');x=subprocess.run([str(GO),'version','-m',str(p)],env=env,capture_output=True,timeout=15);assert x.returncode==0,(name,x.stderr)
 raw=x.stdout;dest=R/'buildinfo'/name;dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(raw)
 lines=raw.decode().splitlines();settings={};module=None;path=None
 for l in lines[1:]:
  parts=l.strip().split('\t')
  if parts[0]=='build':
   k,v=parts[1].split('=',1);assert k not in settings;settings[k]=v
  elif parts[0]=='mod':module=parts[1:]
  elif parts[0]=='path':path=parts[1]
 h,n=bind(p)
 return {'path':str(p),'sha256':h,'bytes':n,'go_version':lines[0].rsplit(': ',1)[-1],'main_package':path,'module':module,'build_settings':settings,'raw_buildinfo_path':str(dest.relative_to(R)),'raw_buildinfo_sha256':sha(raw),'target_executed':False}
local=[]
for n in ['local-loss-reproduction-v1','full-original-distribution-v1','full-original-distribution-v2','full-original-distribution-batch128-v1','full-original-distribution-profile-node2-v1']:
 for binary in ['wukongim','wkcli']:
  p=C/n/'bin'/binary
  if p.is_file():local.append(dict(run=n,role=binary,**metainfo(p,'local-'+n+'-'+binary+'.txt')))
 for n2 in ['supervise.py','launcher.sh','preflight.json','invocation.json','build.log','logs/build-wkcli.log','source-input-integrity.json','input-and-source-recheck.json']:
  p=C/n/n2
  if p.is_file():bind(p)
for n in ['local-loss-reproduction-v2','local-loss-reproduction-v3','local-loss-reproduction-v4']:
 p=C/n/'invocation.json'
 if p.is_file():bind(p)
(R/'local-binary-buildinfo.json').write_text(json.dumps(local,indent=2)+'\n')
meta=json.loads((PUB/'artifact-metadata.json').read_text());manifest=json.loads((PUB/'verified/bundle-manifest.json').read_text());bind(PUB/'artifact-metadata.json');bind(PUB/'verified/bundle-manifest.json');bind(PUB/'verified/verification.json')
zh,zn=bind(Z);assert zn==meta['size_in_bytes'] and 'sha256:'+zh==meta['digest']
expected={f['path']:f for f in manifest['files'] if f['path'].startswith('bin/')};assert len(expected)==9
private=R/'PRIVATE_extracted-cloud';private.mkdir(mode=0o700,exist_ok=True)
entries=[]
with zipfile.ZipFile(Z) as z:
 assert set(z.namelist())=={'cloud-deployment-bundle.tar.gz','cloud-deployment-bundle.tar.gz.sha256','bundle-manifest-output.json'}
 tarinfo=z.getinfo('cloud-deployment-bundle.tar.gz');th=hashlib.sha256();tn=0
 with z.open(tarinfo) as f:
  for b in iter(lambda:f.read(1<<20),b''):th.update(b);tn+=len(b)
 assert z.read('cloud-deployment-bundle.tar.gz.sha256').decode().split()[0]==th.hexdigest()
 with z.open(tarinfo) as f,tarfile.open(fileobj=f,mode='r|gz') as t:
  for m in t:
   name=m.name.removeprefix('./')
   if name not in expected:continue
   assert m.isfile() and not m.issym() and not m.islnk() and m.size==expected[name]['size'] and m.size<=150<<20
   h=hashlib.sha256();size=0;dst=private/pathlib.PurePosixPath(name).name
   with t.extractfile(m) as src,dst.open('wb') as out:
    for b in iter(lambda:src.read(1<<20),b''):h.update(b);size+=len(b);out.write(b)
   dst.chmod(0o600);assert size==m.size and h.hexdigest()==expected[name]['sha256']
   with dst.open('rb') as src:header=src.read(20)
   assert header[:6]==b'\x7fELF\x02\x01' and int.from_bytes(header[18:20],'little')==62
   entry=metainfo(dst,'cloud-'+pathlib.PurePosixPath(name).name+'.txt');entry['tar_member']=name;entry['manifest_entry']=expected[name];entry['elf_class']=64;entry['elf_machine']='x86_64';entry['extracted_only_for_read_only_buildinfo']=True;entries.append(entry)
 assert len(entries)==9
(R/'cloud-binary-buildinfo.json').write_text(json.dumps({'artifact_id':meta['id'],'producer_run':meta['workflow_run'],'zip':{'path':str(Z),'bytes':zn,'sha256':zh},'tar':{'member':tarinfo.filename,'bytes':tn,'sha256':th.hexdigest(),'zip_member_crc32':format(tarinfo.CRC,'08x')},'manifest_source_sha':manifest['source_sha'],'manifest_control_sha':manifest['control_sha'],'manifest_bundle_digest':manifest['bundle_digest'],'selected_elf_members':entries,'no_payload_or_target_executed':True,'network_download':False},indent=2)+'\n')
bind(GO);(R/'input-hashes.json').write_text(json.dumps(inputs,indent=2)+'\n')
print(json.dumps({'local':[{k:e[k] for k in ['run','role','sha256','go_version','build_settings']} for e in local],'cloud':[{k:e[k] for k in ['tar_member','sha256','go_version','build_settings']} for e in entries],'input_file_count':len(inputs)}))
