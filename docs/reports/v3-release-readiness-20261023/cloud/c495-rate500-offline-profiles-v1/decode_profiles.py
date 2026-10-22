from pathlib import Path
import gzip,json,collections,hashlib,datetime,io
P=Path('/tmp/wukongim-v3-readiness-20261023/cloud/full-original-distribution-c495-rate500-profile-v1');O=Path('/tmp/wukongim-v3-readiness-20261023/private/c495-rate500-offline-profiles-v1')
def fields(buf):
 at=0
 def vint():
  nonlocal at
  v=0;shift=0
  while True:
   b=buf[at];at+=1;v|=(b&127)<<shift
   if b<128:return v
   shift+=7;assert shift<70
 while at<len(buf):
  tag=vint();f=tag>>3;w=tag&7
  if w==0:v=vint()
  elif w==2:
   n=vint();v=buf[at:at+n];at+=n
  elif w==1:v=buf[at:at+8];at+=8
  elif w==5:v=buf[at:at+4];at+=4
  else:raise ValueError('wire type')
  assert at<=len(buf)
  yield f,w,v
def packed(buf):
 at=0;vals=[]
 while at<len(buf):
  v=0;shift=0
  while True:
   b=buf[at];at+=1;v|=(b&127)<<shift
   if b<128:break
   shift+=7;assert shift<70
  vals.append(v)
 return vals
def stamp(ns):
 sec,nano=divmod(ns,10**9)
 return datetime.datetime.fromtimestamp(sec,datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S')+'.'+str(nano).zfill(9)+'Z'
def load(kind):
 path=P/f'profiles-node2/profiles/node-2-{kind}.pb.gz';raw=path.read_bytes();
 with gzip.GzipFile(fileobj=io.BytesIO(raw)) as compressed:
  b=compressed.read(4*1024*1024+1)
 assert len(b)<=4*1024*1024
 st=[];functions={};locations={};samples=[];header={};types=[]
 for f,w,v in fields(b):
  if f==6:st.append(v.decode())
  elif f==5:
   d={k:a for k,_,a in fields(v)};functions[d[1]]=d
  elif f==4:
   d={};lines=[]
   for k,_,a in fields(v):
    if k==4:lines.append({c:z for c,_,z in fields(a)})
    elif k==1:d['id']=a
   d['lines']=lines;locations[d['id']]=d
  elif f==2:
   ids=[];vals=[]
   for k,wire,a in fields(v):
    if k in [1,2]:
     target=ids if k==1 else vals
     target.extend(packed(a) if wire==2 else [a])
   samples.append({'ids':ids,'values':vals})
  elif f in [9,10,12]:header[f]=v
  elif f==1:types.append({a:c for a,_,c in fields(v)})
 result=[]
 for sample in samples:
  frames=[]
  for lid in sample['ids']:
   for ln in locations[lid]['lines']:
    fn=functions[ln[1]]
    # Only immutable symbol names and source file/line: never decode labels,
    # print string tables, addresses, raw goroutine IDs or message identities.
    name=st[fn[2]];file=st[fn[4]]
    frames.append({'function':name,'file':file,'line':ln.get(2,0)})
  result.append({'values':sample['values'],'frames':frames})
 return {'file_sha256':hashlib.sha256(raw).hexdigest(),'bytes':len(raw),'time_utc':stamp(header[9]),'time_unix_nanos':str(header[9]),'duration_nanos':str(header.get(10,0)),'end_utc':stamp(header[9]+header.get(10,0)),'period':header.get(12),'types':[{'type':st[t[1]],'unit':st[t[2]]} for t in types]},result

if __name__=='__main__':
 for kind in ['goroutine','cpu']:
  meta,stacks=load(kind)
  (O/f'{kind}-symbol-stacks.json').write_text(json.dumps({'metadata':meta,'stacks':stacks},indent=2)+'\n')
 print('decoded two profiles; private symbol stacks only; labels ignored')
