"""Fixed trace-profile envelope and private bounded process drain; no state extractor."""
from pathlib import Path
import collections,gzip,hashlib,io,json,os,re,selectors,signal,subprocess,time
MAX_I64=(1<<63)-1
TYPES=[{'type':'contentions','unit':'count'},{'type':'delay','unit':'nanoseconds'}]
class OfflineError(ValueError):pass
class _ControlledStop(Exception):pass
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


def _decode_core(b):
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
    name=st[fn.get(2,0)];file=st[fn.get(4,0)]
    frames.append({'function':name,'file':file,'line':ln.get(2,0)})
  result.append({'values':sample['values'],'frames':frames})
 return header,types,st,result

SYMBOLS=frozenset(['github.com/WuKongIM/WuKongIM/pkg/transport/internal/conn.(*Conn).Call', 'github.com/WuKongIM/WuKongIM/pkg/transport/internal/conn.(*Conn).Send', 'github.com/WuKongIM/WuKongIM/pkg/transport/internal/conn.(*Conn).readLoop', 'github.com/WuKongIM/WuKongIM/pkg/transport/internal/conn.(*Conn).writeLoop', 'github.com/WuKongIM/WuKongIM/pkg/transport/internal/conn.(*Conn).writeOutboundBatch', 'github.com/WuKongIM/WuKongIM/internal/usecase/presence.(*AuthorityRecovery).recoverPage', 'github.com/WuKongIM/WuKongIM/internal/usecase/presence.(*AuthorityRecovery).admitRecoveryPage', 'github.com/WuKongIM/WuKongIM/internal/usecase/presence.(*AuthorityRecovery).executeRecoveryCohort', 'github.com/WuKongIM/WuKongIM/internal/infra/cluster.(*PresenceRecoveryOwners).readOwnerPage', 'github.com/WuKongIM/WuKongIM/internal/infra/cluster.(*PresenceRecoveryOwners).RecoverRoutesByTargets', 'github.com/WuKongIM/WuKongIM/internal/access/node.(*Client).ReadOwnerRoutes', 'github.com/WuKongIM/WuKongIM/internal/access/node.(*Client).ReadOwnerRoutesByTargets', 'github.com/WuKongIM/WuKongIM/internal/runtime/online.(*Registry).ActiveRoutesByUIDs'])

def _fail(code): raise OfflineError(code)
def _valid_values(values):
 if len(values)!=2 or any(type(v) is not int or v<0 or v>MAX_I64 for v in values):_fail('profile_invalid')
def decode_trace_profile(path):
 try:
  path=Path(path)
  if path.is_symlink() or not path.is_file() or path.stat().st_size>4194304:_fail('profile_invalid')
  raw=path.read_bytes()
  if len(raw)>4194304:_fail('decode_limit')
  with gzip.GzipFile(fileobj=io.BytesIO(raw)) as z:b=z.read(4194305)
  if len(b)>4194304:_fail('decode_limit')
  # Envelope/wire validation adds no label decoding; the core below is unchanged.
  for field,wire,value in fields(b):
   if field<=0 or field in [1,2,4,5,6] and wire!=2 or field in [9,10,12] and wire!=0:_fail('profile_invalid')
  header,types,strings,records=_decode_core(b)
  sample_types=[{'type':strings[t[1]],'unit':strings[t[2]]} for t in types]
  if sample_types!=TYPES:_fail('profile_invalid')
  for r in records:_valid_values(r['values'])
  for key in [9,10]:
   if key in header and (type(header[key]) is not int or not 0<=header[key]<=MAX_I64):_fail('profile_invalid')
  return {'file_sha256':hashlib.sha256(raw).hexdigest(),'bytes':len(raw),'types':sample_types,'time_unix_nanos':None if 9 not in header else str(header[9]),'duration_nanos':None if 10 not in header else str(header[10])},records
 except OfflineError:raise
 except Exception as error:raise OfflineError('profile_invalid') from error

def aggregate_trace_profile(meta,records,*,node_id,profile_type,coverage):
 if type(node_id) is not int or node_id not in [1,2,3] or profile_type not in ['sched','sync','net','syscall'] or coverage not in ['full_reader_accepted','prefix_or_unknown','missing','partial']:_fail('projection_invalid')
 if meta.get('types')!=TYPES:_fail('profile_invalid')
 flat=collections.defaultdict(lambda:[0,0]);overlap=collections.defaultdict(lambda:[0,0]);count=delay=0
 for row in records:
  _valid_values(row['values']);n,d=row['values'];count+=n;delay+=d
  if count>MAX_I64 or delay>MAX_I64:_fail('profile_invalid')
  known=[frame['function'] for frame in row['frames'] if frame['function'] in SYMBOLS]
  key=known[0] if known else 'other';flat[key][0]+=n;flat[key][1]+=d
  for key in set(known):overlap[key][0]+=n;overlap[key][1]+=d
 def rows(data):return [{'function':k,'count':v[0],'delay_ns':v[1]} for k,v in sorted(data.items())]
 return {'node_id':node_id,'profile_type':profile_type,'coverage':coverage,'aggregate_record_count':len(records),'total_contentions_count':count,'total_delay_ns':delay,'flat_disjoint_known_symbol_or_other':rows(flat),'overlapping_known_symbol_counts_and_delay_explicitly_not_additive':rows(overlap)}

def _safe_path(path):
 path=Path(path)
 for p in [path,*path.parents]:
  if p.is_symlink() and not (str(p)=='/tmp' and str(p.resolve())=='/private/tmp' or str(p)=='/var' and str(p.resolve())=='/private/var'):_fail('unsafe_path')
 return path

def _sha(path):
 h=hashlib.sha256()
 with path.open('rb') as f:
  while block:=f.read(1048576):h.update(block)
 return h.hexdigest()

def verify_trace_input(row,allowed):
 try:
  node=row['node_id'];path=Path(row['path'])
  if type(node) is not int or node not in [1,2,3] or node not in allowed or path!=allowed[node]:_fail('unsafe_path')
  _safe_path(path)
  if not path.is_file():_fail('missing')
  s=path.stat()
  if not 0<s.st_size<=67108864:_fail('input_changed')
  actual={'sha256':_sha(path),'bytes':s.st_size,'mtime_ns':s.st_mtime_ns,'device':s.st_dev,'inode':s.st_ino}
  after=path.stat()
  if (after.st_size,after.st_mtime_ns,after.st_dev,after.st_ino)!=(s.st_size,s.st_mtime_ns,s.st_dev,s.st_ino) or any(actual[k]!=row[k] for k in actual):_fail('input_changed')
  return actual
 except OfflineError:raise
 except Exception as error:raise OfflineError('input_changed') from error

def verify_tool(path,expected):
 path=_safe_path(path)
 if not path.is_file() or not re.fullmatch('[0-9a-f]{64}',expected) or _sha(path)!=expected:_fail('tool_changed')
 return expected

def coverage_from_reader(result):
 return 'full_reader_accepted' if result.get('status')=='complete' and result.get('error_class')=='ok' and result.get('exit_status')==0 and result.get('joined') is True and result.get('stdout_eof') is True and result.get('stderr_eof') is True else 'prefix_or_unknown'

def _private_target(path):
 path=_safe_path(path);parent=path.parent
 if path.exists() or path.is_symlink() or not parent.is_dir() or parent.stat().st_uid!=os.getuid() or parent.stat().st_mode&0o077:_fail('unsafe_path')
 return path

def run_bounded(argv,env,*,stdout_path,stderr_path,stdout_cap,stderr_cap,timeout_seconds,stop_requested=None):
 # Paths are checked before creating either output or any child.
 targets={'stderr':_private_target(stderr_path)}
 if stdout_path is not None:targets['stdout']=_private_target(stdout_path)
 if any(type(n) is not int or n<1 for n in [stdout_cap,stderr_cap]) or not 0<timeout_seconds<=30:_fail('projection_invalid')
 streams={};process=None;selector=selectors.DefaultSelector();reason='ok';start=time.monotonic();deadline=start+timeout_seconds
 counts={'stdout':0,'stderr':0};eof={'stdout':False,'stderr':False};hashes={k:hashlib.sha256() for k in counts};joined=True;exit_status=None;stopped=stop_requested or (lambda:False)
 try:
  if stopped():raise _ControlledStop()
  for k,path in targets.items():streams[k]=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
  process=subprocess.Popen(argv,env=dict(env),stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
  for k,pipe in [('stdout',process.stdout),('stderr',process.stderr)]:os.set_blocking(pipe.fileno(),False);selector.register(pipe,selectors.EVENT_READ,k)
  while selector.get_map():
   if stopped() or time.monotonic()>=deadline:reason='timeout';break
   for key,_ in selector.select(min(.1,max(0,deadline-time.monotonic()))):
    try:data=os.read(key.fd,65536)
    except BlockingIOError:continue
    name=key.data
    if not data:eof[name]=True;selector.unregister(key.fileobj);continue
    cap=stdout_cap if name=='stdout' else stderr_cap;keep=data[:max(0,cap-counts[name])]
    if keep:
     if name in streams:
      at=0
      while at<len(keep):
       wrote=os.write(streams[name],keep[at:])
       if wrote<=0:raise OSError('short_write')
       at+=wrote
     hashes[name].update(keep);counts[name]+=len(keep)
    if len(keep)<len(data):reason=name+'_limit';break
   if reason!='ok':break
  if reason=='ok':
   while process.poll() is None:
    if stopped() or time.monotonic()>=deadline:reason='timeout';break
    try:process.wait(timeout=min(.1,max(.001,deadline-time.monotonic())))
    except subprocess.TimeoutExpired:pass
 except _ControlledStop:reason='timeout'
 except Exception:reason='io_failure'
 finally:
  if process is not None:
   joined=False
   if reason!='ok' or process.poll() is None:
    try:os.killpg(process.pid,signal.SIGKILL)
    except ProcessLookupError:pass
   try:process.wait(timeout=1);joined=True
   except subprocess.TimeoutExpired:reason='unjoined'
   exit_status=process.returncode
   for pipe in [process.stdout,process.stderr]:pipe.close()
  selector.close()
  for fd in streams.values():os.close(fd)
 if reason=='ok' and exit_status!=0:reason='trace_nonzero'
 return {'status':'complete' if reason=='ok' and joined and all(eof.values()) else 'partial','error_class':reason,'exit_status':exit_status,'pid':None if process is None else process.pid,'joined':joined,'elapsed_seconds':time.monotonic()-start,'stdout_eof':eof['stdout'],'stderr_eof':eof['stderr'],'stdout_stream_bytes':counts['stdout'],'stdout_retained_bytes':counts['stdout'] if stdout_path is not None else 0,'stderr_retained_bytes':counts['stderr'],'stdout_sha256':hashes['stdout'].hexdigest(),'stderr_sha256':hashes['stderr'].hexdigest()}
