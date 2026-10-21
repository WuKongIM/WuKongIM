"""TEST ONLY, fixed native Alma DNF signed A/B protocol comparison.
Only test certs/data RPMs; not production bootstrap or installed product acceptance.
"""
from pathlib import Path
import importlib.util,sys,subprocess,hashlib,json,shutil,ssl,threading,time,tempfile,os,inspect
from http.server import ThreadingHTTPServer,BaseHTTPRequestHandler
spec=importlib.util.spec_from_file_location('actual_driver','/control/tests/native_stable_clients_drill.py');d=importlib.util.module_from_spec(spec);sys.modules[spec.name]=d;spec.loader.exec_module(d)
root=Path('/tmp/barrier-probe');root.mkdir();shutil.copytree('/probe/signed-public/public',root/'public')
public=root/'public';fact=json.loads((public/'facts.json').read_text());events=[];commands=[];phase='setup';case='setup'
def digest(raw):return hashlib.sha256(raw).hexdigest()
def run(args,timeout=60,environment=None):
 started=time.time();r=subprocess.run(args,capture_output=True,timeout=timeout,env={'PATH':'/usr/sbin:/usr/bin:/sbin:/bin','HOME':'/tmp','LC_ALL':'C','LANG':'C',**(environment or {})})
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
 validator=d.module('scripts/validate-production-package-clients.py')
 versions=run(['rpm','-q','dnf','libdnf','python3-dnf']).decode().splitlines()
 for serial,(case,g,both) in enumerate([('current-explicit-expiry','A',False),('candidate-both-stage-policy','A',True),('fresh-B','B',False)],1):
  phase='prepare'
  for name in ['client-root','repos.d','dnf-cache','dnf-state','rpmdb','bootstrap-rpmdb']:
   p=Path('/tmp')/name
   if p.exists():shutil.rmtree(p)
  for directory in ['/downloads','/etc/pki/rpm-gpg','/etc/yum.repos.d']:
   for p in Path(directory).iterdir():
    if p.is_dir():shutil.rmtree(p)
    else:p.unlink()
  routes.configure({'serial':serial,'generation':g,'mode':'rpm-switch' if g=='A' else 'normal'})
  boot=public/g/'Packages'/('wukongim-release-stable-'+('1.1.1' if g=='A' else '1.1.2')+'-1.noarch.rpm')
  generated=d.download_client_command(validator,'fixed-image',Path('/downloads'),public/'rpm-test.asc','rpm',boot,mode='rpm-switch' if g=='A' else 'normal')
  original_body=generated[-1];body=original_body
  if both:
   before='dnf "${dnf_options[@]}" makecache --refresh'
   assert body.count(before)==1
   body=body.replace(before,'dnf "${dnf_options[@]}" --setopt=wukongim-stable.metadata_expire=-1 makecache --refresh')
  trace=root/(case+'.jsonl')
  environment={'PYTHONPATH':'/probe/instrumentation','WK_DIAG_LOG_PATH':str(trace),'WK_DIAG_DELAY_STORE':'2.1' if g=='A' else '0','WK_TEST_ONLY_CLIENT':'true','WK_TEST_CA_BUNDLE':str(root/'tls.crt'),'WK_BOOTSTRAP_PACKAGE':str(boot),'WK_REPOSITORY_URL':'https://packages.githubim.com'+d.MIRROR}
  phase='actual-native-shell';run(['bash','-c',body],environment=environment)
  package=next(Path('/downloads').glob('*.rpm'));raw=package.read_bytes();selected=next(k for k,v in fact['payloads'].items() if v['sha256']==digest(raw))
  expected='A' if both else 'B';assert selected==expected,(case,selected,expected)
  observed=[e for e in events if e['case']==case]
  payloads=[e for e in observed if '/Packages/' in e['path']]
  assert payloads and all(e['path'].startswith('/snapshots/B/') and e['status']==200 and e['completed'] and e['sha256']==fact['payloads'][selected]['sha256'] for e in payloads)
  traces=[json.loads(line) for line in trace.read_text().splitlines()]
  for item in traces:
   for repo in item['repos']:assert repo['id']=='wukongim-stable' and repo['repo_gpgcheck'] and repo['gpgcheck']
  after_store=[item for item in traces if item['event']=='after-native-store' and 'makecache' in item['argv']][-1]
  second_config=[item for item in traces if item['event']=='configured' and 'install' in item['argv']][0]
  if g=='A':
   assert after_store['persisted_expired']==([] if both else ['wukongim-stable'])
   assert second_config['repos'][0]['metadata_expire']==-1
   assert second_config['repos'][0]['native_expired'] is (not both)
  if both:assert all(e['path'].startswith('/snapshots/A/') for e in observed if '/repodata/' in e['path'])
  results.append({'case':case,'passed':True,'selected':selected,'expected_A_gate':selected=='A','sha256':digest(raw),'native_body_sha256':digest(body.encode()),'shipped_body_sha256':digest(original_body.encode()),'body_modified':both,'traces':traces,'logs':{str(p):p.read_text(errors='replace')[-32768:] for p in Path('/var/log').glob('dnf*.log') if p.is_file() and p.stat().st_size<262144}})

finally:
 server.shutdown();server.server_close();worker.join(timeout=5);assert not worker.is_alive()
 print(json.dumps({'test_only':True,'scope':'Real stable bootstrap built by unchanged producer, real signed data-only RPM and repository, exact shipped native Rocky shell except explicitly marked comparison. HTTPS loopback/network none. No product/source-release claim. Delay before actual DNF persistence is deliberate, not an observed original CI timestamp.','versions':versions,'facts':fact,'results':results,'commands':commands,'events':events},indent=2))
