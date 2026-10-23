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


def load_profile(path):
 path=Path(path);raw=path.read_bytes();
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

PREFIX='github.com/WuKongIM/WuKongIM/'
def has(r,s):return any(f['function']==PREFIX+s for f in r['frames'])
def line(r,s,n):return any(f['function']==PREFIX+s and f['line']==n for f in r['frames'])
def appfirst(r):
 for f in r['frames']:
  if f['function'].startswith(PREFIX) and not any(t in f['function'] for t in ['/pkg/transport','/pkg/cluster/net','/pkg/cluster.(*Node).CallRPC','/pkg/cluster.(*Node).RPCService']):return f['function'].removeprefix(PREFIX),f['line']
 return 'other',0

def _classify_core(meta,stacks,cpu,cpu_stacks):
 worker=collections.Counter();recovery=collections.Counter();owner_context=collections.Counter();owner_operation=collections.Counter();owner_line=collections.Counter();rpc=collections.Counter();enqueue=collections.Counter();selected_lines=collections.Counter();apply=0
 for r in stacks:
  n=r['values'][0]
  if has(r,'internal/runtime/delivery.(*Runtime).runWorker'):
   if has(r,'internal/runtime/delivery.(*orderedPlanQueue).dequeue'):kind='idle_plan_dequeue'
   elif line(r,'internal/usecase/presence.(*AuthorityRecovery).admitRecoveryPage',156):kind='presence_registry_quota_wait'
   elif has(r,'internal/infra/cluster.(*PresenceRecoveryOwners).RecoverRoutesByTargets'):kind='presence_owner_recovery_RPC'
   elif has(r,'internal/access/node.(*Client).EndpointsByTargets'):kind='presence_remote_authority_RPC'
   elif has(r,'internal/infra/cluster.runBoundedPresenceLeaderBatches') and any(f['function']=='sync.(*WaitGroup).Wait' for f in r['frames']):kind='presence_parallel_leader_join'
   elif has(r,'internal/access/node.(*Client).PushBatch'):kind='owner_push_RPC'
   elif line(r,'internal/usecase/presence.(*AuthorityRecovery).recoverPage',70):kind='presence_UID_flight_join'
   else:kind='unclassified'
   worker[kind]+=n
  if has(r,'internal/usecase/presence.(*AuthorityRecovery).recoverPage'):
   if line(r,'internal/usecase/presence.(*AuthorityRecovery).admitRecoveryPage',156):kind='registry_quota_wait'
   elif has(r,'internal/infra/cluster.(*PresenceRecoveryOwners).RecoverRoutesByTargets') and has(r,'pkg/transport/internal/conn.(*Conn).Call'):kind='executing_cohort_owner_RPC'
   elif line(r,'internal/usecase/presence.(*AuthorityRecovery).recoverPage',70):kind='UID_flight_join'
   else:kind='unclassified'
   recovery[kind]+=n
   for f in r['frames']:
    if 'internal/usecase/presence.(*AuthorityRecovery)' in f['function'] or 'internal/infra/cluster.(*PresenceRecoveryOwners)' in f['function']:
     selected_lines[f['function'].removeprefix(PREFIX),f['line']]+=n
  if has(r,'internal/usecase/presence.(*AuthorityRecovery).executeRecoveryCohort') and has(r,'internal/infra/cluster.(*PresenceRecoveryOwners).RecoverRoutesByTargets'):
   assert has(r,'pkg/transport/internal/conn.(*Conn).Call')
   if has(r,'internal/runtime/delivery.(*Runtime).runWorker'):kind='delivery_worker'
   elif has(r,'internal/access/node.(*Adapter).handlePresenceEndpointsByTargets'):kind='inbound_presence_RPC'
   else:kind='other'
   owner_context[kind]+=n
   if has(r,'internal/access/node.(*Client).ReadOwnerRoutesByTargets'):owner_operation['batch_ReadOwnerRoutesByTargets']+=n
   elif has(r,'internal/access/node.(*Client).ReadOwnerRoutes'):owner_operation['single_ReadOwnerRoutes']+=n
   else:owner_operation['other']+=n
   for f in r['frames']:
    if f['function']==PREFIX+'internal/infra/cluster.(*PresenceRecoveryOwners).readOwnerPage':owner_line[str(f['line'])]+=n
  if has(r,'pkg/transport/internal/conn.(*Conn).Call'):rpc[appfirst(r)]+=n
  if has(r,'internal/runtime/delivery.(*orderedPlanQueue).enqueue'):enqueue['enqueue_caller']+=n
  if has(r,'pkg/slot/multiraft.(*slot).runApplyTask'):apply+=n
 for key in ['presence_UID_flight_join','unclassified']:worker.setdefault(key,0)
 for key in ['UID_flight_join','unclassified']:recovery.setdefault(key,0)
 flat=collections.Counter();cum=collections.Counter()
 for r in cpu_stacks:
  amount=r['values'][1]
  if r['frames']:flat[r['frames'][0]['function']]+=amount
  for name in set(f['function'] for f in r['frames']):cum[name]+=amount
 summary={
  'schema':'wukongim/offline-profile-aggregate/v1','goroutine_profile':meta,'cpu_profile':cpu,
  'goroutines_total':sum(r['values'][0] for r in stacks),'aggregated_stack_records':len(stacks),
  'delivery_runWorker_total':sum(worker.values()),'delivery_runWorker_disjoint':dict(worker),
  'recoverPage_total':sum(recovery.values()),'recoverPage_disjoint':dict(recovery),
  'actual_owner_read_holder_total':sum(owner_context.values()),'actual_owner_read_holder_context_disjoint':dict(owner_context),'actual_owner_read_operation_disjoint':dict(owner_operation),'actual_owner_read_source_line_disjoint':dict(owner_line),
  'Conn_Call_total':sum(rpc.values()),'Conn_Call_nearest_non_transport_product_frame_disjoint':[{'function':fn,'line':ln,'count':n} for (fn,ln),n in rpc.most_common()],
  'delivery_enqueue_callers':sum(enqueue.values()),'Slot_runApplyTask_callers':apply,
  'recovery_symbol_line_aggregates_overlapping':[{'function':fn,'line':ln,'count':n} for (fn,ln),n in selected_lines.items()],
  'cpu_sample_count':sum(r['values'][0] for r in cpu_stacks),'cpu_total_sample_nanoseconds':sum(r['values'][1] for r in cpu_stacks),
  'cpu_flat_top_20':[{'function':fn,'nanoseconds':v} for fn,v in flat.most_common(20)],
  'limits':'Disjoint only within named cohorts; cohorts overlap. One node at one point. Source line proves which branch is sampled, not quota dimension, elapsed waits, remote serving cause, sampled-message identity or Linux/whole-window capacity. CPU nanoseconds are on-CPU samples, never disk/off-CPU latency. No raw stack records/labels/identities are published.'}
 return summary


class AdapterError(ValueError): pass

def fixed_paths(root):
 return {(node,kind):Path(root)/f'profiles-three-node/node-{node}/profiles/node-{node}-{kind}.pb.gz' for node in [1,2,3] for kind in ['cpu','goroutine']}

def verify_input(row,allowed):
 path=Path(row['path']);key=(row['node_id'],row['kind'])
 if key not in allowed or path!=allowed[key]:raise AdapterError('unapproved_path')
 if any(p.is_symlink() and not (str(p)=='/tmp' and str(p.resolve())=='/private/tmp' or str(p)=='/var' and str(p.resolve())=='/private/var') for p in [path,*path.parents]):raise AdapterError('unsafe_symlink')
 if not path.is_file():raise AdapterError('missing')
 stat=path.stat();limit=8388608 if row['kind']=='cpu' else 4194304
 if stat.st_size>limit:raise AdapterError('input_oversized')
 data=path.read_bytes();actual={'sha256':hashlib.sha256(data).hexdigest(),'bytes':len(data),'mtime_ns':stat.st_mtime_ns}
 if any(actual[k]!=row[k] for k in actual):raise AdapterError('input_mismatch')
 return actual

def verify_types(kind,meta,stacks):
 expected=[{'type':'goroutine','unit':'count'}] if kind=='goroutine' else [{'type':'samples','unit':'count'},{'type':'cpu','unit':'nanoseconds'}]
 if meta['types']!=expected:raise AdapterError('sample_type_mismatch')
 if any(len(r['values'])!=len(expected) or any(not isinstance(x,int) or x<0 for x in r['values']) for r in stacks):raise AdapterError('sample_shape')

def classify_goroutine(meta,stacks):
 summary=_classify_core(meta,stacks,{},[])
 summary={k:v for k,v in summary.items() if k!='cpu_profile' and not k.startswith('cpu_')}
 cohorts=[('delivery_runWorker_total','delivery_runWorker_disjoint'),('recoverPage_total','recoverPage_disjoint'),('actual_owner_read_holder_total','actual_owner_read_holder_context_disjoint')]
 if any(summary[total]!=sum(summary[parts].values()) for total,parts in cohorts):raise AdapterError('partition_invariant')
 if summary['delivery_runWorker_total']!=sum(r['values'][0] for r in stacks if has(r,'internal/runtime/delivery.(*Runtime).runWorker')):raise AdapterError('partition_invariant')
 return summary

def classify_cpu(meta,stacks):
 summary=_classify_core({},[],meta,stacks)
 return {k:v for k,v in summary.items() if k=='cpu_profile' or k.startswith('cpu_')}

def bounded_json(value,maximum):
 data=(json.dumps(value,indent=2)+'\n').encode()
 if len(data)>maximum:raise AdapterError('output_oversized')
 return data

def closed_error(error):
 return str(error) if isinstance(error,AdapterError) else type(error).__name__
