"""TEST ONLY, fixed native Alma DNF signed A/B protocol comparison.
Only test certs/data RPMs; not production bootstrap or installed product acceptance.
"""
from pathlib import Path
import importlib.util,sys,subprocess,hashlib,json,shutil,ssl,threading,time,tempfile
from http.server import ThreadingHTTPServer,BaseHTTPRequestHandler
spec=importlib.util.spec_from_file_location('actual_driver','/control/tests/native_stable_clients_drill.py');d=importlib.util.module_from_spec(spec);sys.modules[spec.name]=d;spec.loader.exec_module(d)
root=Path('/tmp/barrier-probe');root.mkdir();shutil.copytree('/probe/signed-public/public',root/'public')
public=root/'public';fact=json.loads((public/'facts.json').read_text());events=[];commands=[];phase='setup';case='setup'
def digest(raw):return hashlib.sha256(raw).hexdigest()
def run(args,timeout=45):
 started=time.time();r=subprocess.run(args,capture_output=True,timeout=timeout,env={'PATH':'/usr/sbin:/usr/bin:/sbin:/bin','HOME':'/tmp','LC_ALL':'C','LANG':'C'})
 assert len(r.stdout)+len(r.stderr)<262144
 commands.append({'case':case,'phase':phase,'command':list(map(str,args)),'started_at':started,'finished_at':time.time(),'exit_code':r.returncode,'stdout':r.stdout.decode(errors='replace'),'stderr':r.stderr.decode(errors='replace')})
 assert r.returncode==0,(args,r.stderr.decode());return r.stdout
run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-subj','/CN=packages.githubim.com','-addext','subjectAltName=DNS:packages.githubim.com','-keyout',str(root/'tls.key'),'-out',str(root/'tls.crt')])
maps={}
for g in ['A','B']:
 (public/('mirror-'+g)).write_text('https://packages.githubim.com/snapshots/'+g+'/site/rpm/stable/el/9/x86_64/\n')
 maps[g]={d.MIRROR:{'file':'mirror-'+g}}
 for source in ['A','B']:
  for p in (public/source).rglob('*'):
   if p.is_file():maps[g]['/snapshots/'+source+'/site/rpm/stable/el/9/x86_64/'+p.relative_to(public/source).as_posix()]={'file':p.relative_to(public).as_posix()}
routes=d.RouteTable(public,maps)
class Server(ThreadingHTTPServer):
 def shutdown_request(self,request):
  try:request.settimeout(2);request=request.unwrap()
  except OSError:pass
  finally:super().shutdown_request(request)
class Handler(BaseHTTPRequestHandler):
 def log_message(self,*a):pass
 def do_GET(self):
  status,raw,headers=routes.response(self.path,self.headers)
  event={'case':case,'phase':phase,'path':self.path,'status':status,'size':len(raw),'sha256':digest(raw),'completed':False}
  try:
   self.send_response(status)
   for k,v in headers.items():self.send_header(k,v)
   self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw);self.wfile.flush();event['completed']=True
  finally:events.append(event)
server=Server(('127.0.0.1',443),Handler);ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain(root/'tls.crt',root/'tls.key');server.socket=ctx.wrap_socket(server.socket,server_side=True)
worker=threading.Thread(target=server.serve_forever,daemon=True);worker.start();results=[]
try:
 for serial,(case,g,strategy) in enumerate([('original-expired-A','A',False),('explicit-A-stage','A',True),('fresh-B','B',False)],1):
  phase='prepare';paths=root/case
  for x in ['repos','cache','state','downloads','rpmdb','client-root/var/lib/rpm']:(paths/x).mkdir(parents=True)
  shutil.copytree('/var/lib/rpm',paths/'client-root/var/lib/rpm',dirs_exist_ok=True)
  repo=paths/'repos/wukongim-stable.repo'
  repo.write_text('[wukongim-stable]\nname=TEST ONLY stable barrier\nmirrorlist=https://packages.githubim.com'+d.MIRROR+'\nenabled=1\nincludepkgs=wukongim\ngpgcheck=1\nrepo_gpgcheck=1\ngpgkey=file://'+str(public/'rpm-test.asc')+'\nsslverify=1\nmetadata_expire=0\nskip_if_unavailable=0\n')
  routes.configure({'serial':serial,'generation':g,'mode':'rpm-switch' if g=='A' else 'normal'})
  opts=['dnf','--quiet','--assumeyes','--installroot='+str(paths/'client-root'),'--releasever=9','--disablerepo=*','--enablerepo=wukongim-stable','--setopt=reposdir='+str(paths/'repos'),'--setopt=cachedir='+str(paths/'cache'),'--setopt=persistdir='+str(paths/'state'),'--setopt=keepcache=False','--setopt=install_weak_deps=False','--setopt=wukongim-stable.sslcacert='+str(root/'tls.crt')]
  phase='makecache-A' if g=='A' else 'makecache-B';run(opts+['makecache','--refresh'])
  primary=list((paths/'cache').rglob('primary.xml.gz'));assert len(primary)==1,primary
  before={'mtime_ns':primary[0].stat().st_mtime_ns,'now':time.time(),'sha256':digest(primary[0].read_bytes())}
  if g=='A':time.sleep(2.1)
  phase='download-A-selected' if strategy else 'download-normal'
  aged={'mtime_ns':primary[0].stat().st_mtime_ns,'now':time.time(),'sha256':digest(primary[0].read_bytes())}
  if g=='A':assert int(aged['now'])-int(aged['mtime_ns']/1e9)>=2
  run(opts+(['--setopt=wukongim-stable.metadata_expire=-1'] if strategy else [])+['install','--downloadonly','--downloaddir='+str(paths/'downloads'),'wukongim'])
  packages=list((paths/'downloads').glob('*.rpm'));assert len(packages)==1
  phase='verify';run(['rpm','--dbpath',str(paths/'rpmdb'),'--initdb']);run(['rpmkeys','--dbpath',str(paths/'rpmdb'),'--import',str(public/'rpm-test.asc')]);signature=run(['rpmkeys','--dbpath',str(paths/'rpmdb'),'--checksig',str(packages[0])]).decode();assert signature.rstrip().endswith('digests signatures OK')
  version=run(['rpm','-qp','--queryformat','%{VERSION}',str(packages[0])]).decode();raw=packages[0].read_bytes();selected=next(k for k,v in fact['payloads'].items() if v['sha256']==digest(raw));expected='A' if strategy else 'B'
  assert selected==expected,(case,selected,expected)
  ev=[e for e in events if e['case']==case];mirror=[e for e in ev if e['path']==d.MIRROR]
  payload=[e for e in ev if '/Packages/' in e['path']];assert payload and all(e['status']==200 and e['completed'] for e in payload)
  assert any(e['sha256']==digest((public/'mirror-B').read_bytes()) for e in mirror)
  assert all(e['path'].startswith('/snapshots/B/') and e['sha256']==fact['payloads'][selected]['sha256'] for e in payload)
  metadata=[e for e in ev if '/repodata/' in e['path']]
  if strategy:
   assert metadata and all(e['path'].startswith('/snapshots/A/') for e in metadata)
   assert all(e['phase']=='makecache-A' for e in metadata)
  results.append({'case':case,'passed':True,'selected':selected,'version':version,'sha256':digest(raw),'signature':signature.strip(),'before':before,'aged':aged,'exact_A_assertion_passes':selected=='A','B_mirror_and_B_payload_prefix':True,'metadata_paths':sorted(set(e['path'] for e in metadata))})
finally:
 server.shutdown();server.server_close();worker.join(timeout=5);assert not worker.is_alive()
 print(json.dumps({'test_only':True,'scope':'Real signed data-only RPM and signed repository, native Alma image, HTTPS loopback only. No production bootstrap/product/source-release claim. Age is deliberate in this probe, not an observed original CI timestamp.','facts':fact,'results':results,'commands':commands,'events':events},indent=2))
