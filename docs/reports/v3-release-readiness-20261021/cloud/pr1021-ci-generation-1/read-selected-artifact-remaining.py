"""Read selected small ZIP entries through bounded gh api byte-range requests."""
from pathlib import Path
import io,json,subprocess,sys,zipfile,urllib.request,urllib.error,urllib.parse
root=Path(__file__).parent.resolve()/"large-artifact-read2"; root.mkdir(exist_ok=True); artifact=int(sys.argv[1]); size=int(sys.argv[2])
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs):return None
opener=urllib.request.build_opener(NoRedirect)
request=urllib.request.Request(f'https://api.github.com/repos/WuKongIM/WuKongIM/actions/artifacts/{artifact}/zip',headers={'Authorization':'Bearer '+subprocess.check_output(['gh','auth','token'],text=True).strip(),'Accept':'application/vnd.github+json'})
try:
 response=opener.open(request,timeout=30)
 raise RuntimeError('artifact endpoint did not redirect')
except urllib.error.HTTPError as error:
 if error.code!=302:raise RuntimeError(f'artifact metadata response {error.code}') from None
 location=error.headers['Location']
parsed=urllib.parse.urlparse(location)
if parsed.scheme!='https' or not parsed.hostname.endswith('.blob.core.windows.net'):raise RuntimeError('unexpected artifact storage host')
class Ranged(io.RawIOBase):
 def __init__(self):self.position=0;self.fetched=0
 def seekable(self):return True
 def readable(self):return True
 def tell(self):return self.position
 def seek(self,offset,whence=0):
  self.position=offset if whence==0 else self.position+offset if whence==1 else size+offset
  if self.position<0:raise ValueError('negative seek')
  return self.position
 def read(self,count=-1):
  if count<0:count=size-self.position
  count=min(count,size-self.position)
  if count<=0:return b''
  if count>4*1024*1024 or self.fetched+count>12*1024*1024:raise RuntimeError('selective artifact read exceeds audit budget')
  request=urllib.request.Request(location,headers={'Range':f'bytes={self.position}-{self.position+count-1}'})
  try:
   with urllib.request.urlopen(request,timeout=30) as response:
    if response.status!=206:raise RuntimeError('storage did not honor bounded range')
    if response.headers.get('Content-Range')!=f'bytes {self.position}-{self.position+count-1}/{size}':raise RuntimeError('range identity mismatch')
    data=response.read(count+1)
    if len(data)!=count:raise RuntimeError('range length mismatch')
  except Exception as error:
   raise RuntimeError(f'bounded artifact range failed: {type(error).__name__}') from None
  self.position+=count;self.fetched+=count;return data
reader=Ranged()
with zipfile.ZipFile(reader) as z:
 inventory=[{'path':i.filename,'size':i.file_size,'compressed_size':i.compress_size} for i in z.infolist()]
 (root/f'artifact-{artifact}-inventory.json').write_text(json.dumps(inventory,indent=2)+'\n')
 print(json.dumps({'artifact':artifact,'entries':len(inventory),'json_entries':[i for i in inventory if i['path'].endswith('.json') and i['size']<500000]}))
 selected=[]
 for item in z.infolist():
  if (item.filename in {'kernel.txt','data-filesystem.json','artifacts/evidence/graceful-stop-status.json'}) and item.file_size<4*1024*1024:
   path=root/f'artifact-{artifact}-selected'/item.filename
   assert path.resolve().is_relative_to(root)
   path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(z.read(item));selected.append(item.filename)
 (root/f'artifact-{artifact}-selection.json').write_text(json.dumps({'artifact_id':artifact,'archive_size':size,'downloaded_range_bytes':reader.fetched,'selected_entries':selected,'validation':'GitHub-authenticated blob byte ranges; ZIP entry CRC checked; entire archive SHA256 not independently checked'},indent=2)+'\n')
 print('Fetched range bytes:',reader.fetched)
