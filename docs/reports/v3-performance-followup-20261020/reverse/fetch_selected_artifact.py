#!/usr/bin/env python3
"""Read only selected small files from one exact GitHub Actions ZIP artifact."""
import io, json, pathlib, subprocess, urllib.request, urllib.error, zipfile

BASE = pathlib.Path('/tmp/wukongim-performance-followup-20261020/reverse')
REPO = 'WuKongIM/WuKongIM'
RUN = 39407349500
meta = json.loads(subprocess.check_output(['gh','api',f'repos/{REPO}/actions/runs/{RUN}/artifacts'], text=True))
BASE.joinpath('artifacts.json').write_text(json.dumps(meta, indent=2)+'\n')
artifacts = [a for a in meta['artifacts'] if a['name'].startswith(f'conversation-mixed-{RUN}-') and not a['expired']]
if len(artifacts) != 1:
    raise SystemExit(f'Expected one exact diagnostic artifact, found {len(artifacts)}')
a = artifacts[0]
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
# Credential is retained only in process memory and sent only to api.github.com.
token = subprocess.check_output(['gh','auth','token'], text=True).strip()
req = urllib.request.Request(f'https://api.github.com/repos/{REPO}/actions/artifacts/{a["id"]}/zip',headers={'Authorization':f'Bearer {token}','Accept':'application/vnd.github+json','X-GitHub-Api-Version':'2022-11-28'})
try:
    urllib.request.build_opener(NoRedirect()).open(req, timeout=30)
    raise SystemExit('Expected artifact redirect')
except urllib.error.HTTPError as e:
    if e.code != 302:
        raise
    signed_url = e.headers['Location']
del token, req
class RangeFile(io.RawIOBase):
    def __init__(self, url):
        self.url = url
        self.pos = 0
        self.requests = 0
        self.fetched = 0
        with self.request(0, 0) as r:
            total = r.headers.get('Content-Range', '').rsplit('/',1)[-1]
            self.size = int(total)
            if r.status != 206:
                raise RuntimeError('Range access not honored')
            self.fetched += len(r.read(2))
    def request(self, first, last):
        self.requests += 1
        return urllib.request.urlopen(urllib.request.Request(self.url,headers={'Range':f'bytes={first}-{last}'}),timeout=60)
    def readable(self): return True
    def seekable(self): return True
    def tell(self): return self.pos
    def seek(self, offset, whence=0):
        self.pos = offset if whence == 0 else self.pos+offset if whence == 1 else self.size+offset
        if self.pos < 0: raise ValueError('Negative offset')
        return self.pos
    def read(self, size=-1):
        if size < 0: size = self.size-self.pos
        size = min(size, self.size-self.pos)
        if size <= 0: return b''
        if size > 16*1024*1024: raise RuntimeError('Oversized range refused')
        first = self.pos
        with self.request(first, first+size-1) as r:
            if r.status != 206: raise RuntimeError('Range access not honored')
            raw = r.read(size+1)
        if len(raw) != size: raise RuntimeError('Unexpected range length')
        self.pos += len(raw)
        self.fetched += len(raw)
        return raw
rf = RangeFile(signed_url)
selected = {'report.json','binary.sha256','build.txt','host.json','kernel.txt','test.log','comparison/report.json','comparison/binary.sha256','comparison/build.txt','comparison/test.log'}
selected.update({prefix+name for prefix in ('','comparison/') for name in ('driver-cpu.pprof','node1-cpu.pprof','node2-cpu.pprof','node3-cpu.pprof')})
manifest = []
with zipfile.ZipFile(rf) as z:
    for info in z.infolist():
        if info.filename not in selected: continue
        if info.file_size > 12*1024*1024: raise RuntimeError('Selected file over limit')
        body = z.read(info)
        p = BASE / info.filename
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(body)
        manifest.append({'path':info.filename,'bytes':len(body),'zip_crc32':f'{info.CRC:08x}'})
receipt = {'run_id':RUN,'artifact_id':a['id'],'artifact_name':a['name'],'artifact_archive_digest':a.get('digest'),'archive_bytes':rf.size,'downloaded_bytes':rf.fetched,'range_requests':rf.requests,'files':manifest,'archive_digest_recomputed':False,'binary_downloaded':False}
BASE.joinpath('selective-download.json').write_text(json.dumps(receipt,indent=2)+'\n')
print(json.dumps(receipt, indent=2))
