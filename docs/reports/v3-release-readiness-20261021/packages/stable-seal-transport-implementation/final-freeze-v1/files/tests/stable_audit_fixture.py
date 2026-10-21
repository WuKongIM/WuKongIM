"""Loopback GitHub API substitute; identities here are explicitly TEST ONLY."""
import contextlib
import hashlib
import json
import threading
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

class AuditState:
    def __init__(self, release_id=4242, control='a'*40):
        self.release_id=release_id; self.control=control; self.target=control
        self.tag=f'native-package-stable-r{release_id}'; self.tag_exists=False
        self.assets={}; self.draft=True; self.immutable=False; self.requests=[]
        self.base=None; self.policy=True; self.patches=[]
    def release(self):
        return {'id':self.release_id,'tag_name':self.tag,
            'name':f'WuKongIM stable package snapshot r{self.release_id}',
            'target_commitish':self.target,'draft':self.draft,'immutable':self.immutable,
            'prerelease':True,'published_at':None if self.draft else '2026-10-21T00:00:00Z',
            'upload_url':self.base+f'/repos/WuKongIM/packages/releases/{self.release_id}/assets{{?name,label}}',
            'assets':[{'id':item['id'],'name':name,'state':'uploaded','size':len(item['bytes']),
                       'digest':'sha256:'+hashlib.sha256(item['bytes']).hexdigest()}
                      for name,item in self.assets.items()]}

class Handler(BaseHTTPRequestHandler):
    def log_message(self,*args): pass
    def respond(self,value,status=200):
        raw=json.dumps(value).encode(); self.send_response(status)
        self.send_header('Content-Length',str(len(raw))); self.end_headers(); self.wfile.write(raw)
    def do_GET(self):
        s=self.server.state; s.requests.append(('GET',self.path))
        prefix='/repos/WuKongIM/packages/'
        if self.path==prefix+'git/ref/heads/main':
            return self.respond({'ref':'refs/heads/main','object':{'type':'commit','sha':s.control}})
        if self.path==prefix+'git/ref/tags/'+s.tag:
            if not s.tag_exists: return self.send_error(404)
            return self.respond({'ref':'refs/tags/'+s.tag,'object':{'type':'commit','sha':s.control}})
        if self.path==prefix+'immutable-releases': return self.respond({'enabled':s.policy})
        if self.path==prefix+f'releases/{s.release_id}': return self.respond(s.release())
        if self.path.startswith(prefix+'releases/assets/'):
            asset_id=int(self.path.rsplit('/',1)[1])
            for item in s.assets.values():
                if item['id']==asset_id:
                    raw=item['bytes']; self.send_response(200); self.send_header('Content-Length',str(len(raw)))
                    self.end_headers(); self.wfile.write(raw); return
        self.send_error(404)
    def do_POST(self):
        s=self.server.state;s.requests.append(('POST',self.path))
        raw=self.rfile.read(int(self.headers['Content-Length']))
        prefix='/repos/WuKongIM/packages/'
        if self.path==prefix+'git/refs':
            body=json.loads(raw)
            if body!={'ref':'refs/tags/'+s.tag,'sha':s.control}: return self.send_error(400)
            s.tag_exists=True
            return self.respond({'ref':'refs/tags/'+s.tag,'object':{'type':'commit','sha':s.control}},201)
        parsed=urllib.parse.urlsplit(self.path)
        if parsed.path==prefix+f'releases/{s.release_id}/assets':
            name=urllib.parse.parse_qs(parsed.query)['name'][0]
            if name in s.assets: return self.send_error(422)
            s.assets[name]={'id':9001+len(s.assets),'bytes':raw}
            return self.respond(next(x for x in s.release()['assets'] if x['name']==name),201)
        self.send_error(404)
    def do_PATCH(self):
        s=self.server.state;s.requests.append(('PATCH',self.path))
        body=json.loads(self.rfile.read(int(self.headers['Content-Length'])));s.patches.append(body)
        if set(body)=={'target_commitish'}: s.target=body['target_commitish']
        elif body=={'draft':False,'prerelease':True,'make_latest':'false'}: s.draft=False;s.immutable=True
        else: return self.send_error(400)
        self.respond(s.release())

@contextlib.contextmanager
def serve_audit(state):
    server=ThreadingHTTPServer(('127.0.0.1',0),Handler);server.state=state
    state.base=f'http://127.0.0.1:{server.server_port}'
    thread=threading.Thread(target=server.serve_forever,kwargs={'poll_interval':0.01},daemon=True);thread.start()
    try: yield state.base
    finally: server.shutdown();thread.join(timeout=5);server.server_close()
