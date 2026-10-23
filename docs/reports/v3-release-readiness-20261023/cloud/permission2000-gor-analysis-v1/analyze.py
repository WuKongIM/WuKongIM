"""Bounded offline five-debug2 snapshot classifier; no HTTP, DB, subprocess or CPU decode."""
from pathlib import Path
import collections,hashlib,json,re,datetime,time
B=Path('/tmp/wukongim-v3-readiness-20261023/cloud')
R=B/'full-original-distribution-4cb4-rate2000-three-profile-v1'
O=Path(__file__).resolve().parent
S=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-exact-source-width16-candidate')
H=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
started=time.monotonic()
assert H(R/'PUBLIC_SHA256SUMS')=='5f9dccba5decedeecd54a46da94968f226c4d86a5c865f546f9250cd972dc37b'
assert H(R/'safe-profile-artifact-inventory.json')=='8193187d24bf81c88ee48b00ec943a881f633652143019fb6ecebc75e02f13f1'
inv=json.loads((R/'safe-profile-artifact-inventory.json').read_bytes())
meta=json.loads((R/'safe-sampler-status.json').read_bytes())
repo='github.com/WuKongIM/WuKongIM/'
C={
 'barrier':repo+'pkg/slot/multiraft.(*Runtime).readBarrier',
 'wait_apply':repo+'pkg/slot/multiraft.(*slot).waitApplyIdle',
 'slot_worker':repo+'pkg/slot/multiraft.(*scheduler).processSlot',
 'raft_writer':repo+'pkg/raftlog.(*DB).runWriteWorker',
 'raft_collect':repo+'pkg/raftlog.collectWriteRequests',
 'raft_flush':repo+'pkg/raftlog.(*DB).flushWriteRequests',
 'raft_save':repo+'pkg/raftlog.(*pebbleStore).Save',
 'raft_submit':repo+'pkg/raftlog.(*DB).submitWrite',
 'db_owner':repo+'pkg/db/internal/commit.(*Coordinator).run',
 'db_submit':repo+'pkg/db/internal/commit.(*Coordinator).submitResult',
 'db_collect':repo+'pkg/db/internal/commit.(*Coordinator).collect',
 'db_next':repo+'pkg/db/internal/commit.(*Coordinator).nextRequest',
 'person_worker':repo+'internal/runtime/persondirectory.(*Projector).runWorker',
 'person_project':repo+'internal/runtime/persondirectory.(*Projector).projectBatch',
 'membership':repo+'pkg/cluster.(*Node).EnsureUserChannelMembershipBatch',
 'completion':repo+'pkg/cluster.(*Node).CompletePersonDirectoryTasks',
 'origin_call':repo+'pkg/slot/proxy.(*Store).ReadSendPermissionMetadataBatch',
 'origin_worker':repo+'pkg/slot/proxy.(*Store).runSendPermissionCohort',
 'serving_slot':repo+'pkg/slot/proxy.(*Store).readSendPermissionSlot',
 'serving_admit':repo+'pkg/slot/proxy.(*Store).acquireSendPermissionEnvelope',
 'pebble_publish':'github.com/cockroachdb/pebble/v2.(*commitPipeline).publish',
 'wal_flush':'github.com/cockroachdb/pebble/v2/record.(*LogWriter).flushLoop',
 'wal_sync':'github.com/cockroachdb/pebble/v2/record.(*LogWriter).syncWithLatency',
 'physical_fsync':'internal/poll.(*FD).Fsync',
 'snapshot_fsync':repo+'pkg/raftlog.fsyncDir',
}
allowed_states={'running','runnable','select','chan receive','chan send','syscall','sync.Cond.Wait','sync.WaitGroup.Wait','sync.Mutex.Lock','IO wait','sleep','semacquire'}
priorities=['raftlog_writer','db_commit_owner','wait_apply_idle','Raft_processSlot','permission_serving_barrier','other_read_barrier','DB_commit_submit_result','person_directory_membership_worker','person_directory_completion_worker','person_directory_idle_or_other_worker','origin_permission_caller','origin_permission_cohort_worker','permission_serving_admission','physical_fsync_or_WAL_background','other']
def cls(s):
 if C['raft_writer'] in s:return priorities[0]
 if C['db_owner'] in s:return priorities[1]
 if any(x.startswith(C['wait_apply']) for x in s):return priorities[2]
 if any(x.endswith('.processSlot') and '/pkg/slot/multiraft.' in x for x in s):return priorities[3]
 if C['barrier'] in s:return priorities[4] if C['serving_slot'] in s else priorities[5]
 if C['db_submit'] in s:return priorities[6]
 if C['person_worker'] in s:return priorities[7] if C['membership'] in s else priorities[8] if C['completion'] in s else priorities[9]
 if C['origin_call'] in s:return priorities[10]
 if C['origin_worker'] in s:return priorities[11]
 if C['serving_admit'] in s:return priorities[12]
 if C['physical_fsync'] in s or C['snapshot_fsync'] in s or C['wal_flush'] in s:return priorities[13]
 return priorities[14]
def write(n,v):
 p=O/n;assert not p.exists();p.write_text(json.dumps(v,indent=2)+'\n');p.chmod(0o600)
rows=[];inputs=[];totalbytes=0
for item in inv['files']:
 if not item['kind'].startswith('goroutine') or item['status']!='complete_private_retained':continue
 p=R/item['relative_private_path'];before=(H(p),p.stat().st_size,p.stat().st_mtime_ns)
 assert before==(item['sha256'],item['bytes'],item['mtime_ns']) and p.stat().st_size<=8<<20
 totalbytes+=p.stat().st_size;assert totalbytes<=40<<20
 text=p.read_text();blocks=re.split(r'(?m)(?=^goroutine [0-9]+ \[)',text);counts=collections.Counter();states=collections.Counter();watched=collections.Counter();contexts=collections.defaultdict(collections.Counter);total=0;elided=0;unknownstates=0
 for block in blocks:
  if not block.strip():continue
  h=re.match(r'^goroutine [0-9]+ \[([^\]]+)\]:\n',block);assert h,'unexpected_debug2_header'
  total+=1;state=h[1].split(',')[0];unknownstates+=state not in allowed_states;state=state if state in allowed_states else 'other';states[state]+=1
  lines=block.splitlines();frames=[]
  for i,line in enumerate(lines[:-1]):
   if line.startswith('\t') or line.startswith('created by ') or '(' not in line or not line.endswith(')'):continue
   m=re.match(r'^\t(.+):(\d+)(?: |$)',lines[i+1])
   if m:frames.append((line.rsplit('(',1)[0],m[1],int(m[2])))
  assert frames,'missing_frames';s={f[0] for f in frames};category=cls(s);counts[(category,state)]+=1
  elided+=bool(re.search(r'frames elided',block))
  for symbol,path,line in frames:
   if symbol in C.values() or symbol.startswith(C['wait_apply']):
    if path.startswith(repo):path=path[len(repo):]
    watched[(symbol,path,line,state)]+=1
  # These are overlapping closed cross-check subsets, not the partition above.
  if C['raft_writer'] in s:
   subtype='batch_commit_publish_wait' if C['raft_flush'] in s and C['pebble_publish'] in s else 'collect_cross_scope_batch' if C['raft_collect'] in s else 'idle_writeCh_or_other'
   contexts['raftlog_writer'][(subtype,state)]+=1
  if C['db_owner'] in s:
   subtype='pebble_publish_or_WAL_wait' if C['pebble_publish'] in s else 'collect' if C['db_collect'] in s else 'next_request' if C['db_next'] in s else 'other'
   contexts['DB_commit_owner'][(subtype,state)]+=1
  if C['person_worker'] in s:
   subtype='membership_proposal_wave' if C['membership'] in s else 'completion_proposal' if C['completion'] in s else 'idle_or_other'
   contexts['persondirectory_worker'][(subtype,state)]+=1
  if any(x.endswith('.processSlot') and '/pkg/slot/multiraft.' in x for x in s):
   subtype='shared_raftlog_req_done' if C['raft_submit'] in s else 'wait_apply_idle' if any(x.startswith(C['wait_apply']) for x in s) else 'other_processSlot'
   contexts['Raft_processSlot'][(subtype,state)]+=1
  if C['physical_fsync'] in s or C['snapshot_fsync'] in s:
   subtype='snapshot_directory_fsync' if C['snapshot_fsync'] in s else 'Pebble_WAL_LogWriter_sync' if C['wal_flush'] in s else 'other_fsync'
   contexts['physical_fsync_context'][(subtype,state)]+=1
 assert sum(counts.values())==total==sum(states.values())
 req=next(n for n in meta['nodes'] if n['node']==item['node'])['requests'];rq=next(x for x in req if x['kind']==item['kind'])
 rows.append({'node':item['node'],'cut':item['kind'],'request_submit_after_firstgrant_seconds':rq['submit_after_firstgrant_seconds'],'request_join_after_firstgrant_seconds':rq['join_after_firstgrant_seconds'],'snapshotted_goroutines':total,'state_counts':dict(states),'unknown_state_headers':unknownstates,'goroutines_with_elided_frames':elided,'exclusive_category_state_counts':[{'category':k[0],'state':k[1],'count':v} for k,v in sorted(counts.items())],'watched_symbol_source_line_counts':[{'symbol':k[0],'source':k[1],'line':k[2],'state':k[3],'count':v} for k,v in sorted(watched.items())],'overlapping_closed_subsets':{group:[{'category':k[0],'state':k[1],'count':v} for k,v in sorted(c.items())] for group,c in contexts.items()},'observed_waitApplyIdle_symbol_count':sum(v for k,v in watched.items() if k[0].startswith(C['wait_apply']))})
 assert before==(H(p),p.stat().st_size,p.stat().st_mtime_ns)
 inputs.append({'node':item['node'],'cut':item['kind'],'relative_private_path':item['relative_private_path'],'bytes':before[1],'sha256':before[0],'mtime_ns':before[2],'immutable_after':True})
assert len(rows)==5
write('counts.json',{'schema':'wukongim/local2000-five-gor-closed-analysis/v1','source_revision':'4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac','source_tree':'1f2826553a264053e954229ac2581bcf48afe6ad','inputs':inputs,'exclusive_category_priority':priorities,'observed_files':rows,'node3_late':{'status':'8MiB_streaming_cap_failure_body_deleted','available':False,'counts':None},'elapsed_parse_and_hash_seconds':time.monotonic()-started,'raw_stacks_arguments_IDs_or_full_callchains_public':False,'CPU_profiles_decoded':False,'new_HTTP_DB_product_build_test_operations':0})
paths=['AGENTS.md','pkg/slot/FLOW.md','pkg/db/FLOW.md','pkg/db/meta/FLOW.md','internal/usecase/message/FLOW.md','pkg/cluster/FLOW.md','pkg/slot/multiraft/read_barrier.go','pkg/slot/multiraft/slot.go','pkg/db/internal/commit/coordinator.go','pkg/raftlog/pebble_store.go','pkg/raftlog/pebble_writer.go','pkg/raftlog/pebble_db.go','internal/runtime/persondirectory/projector.go']
external=Path('/Users/tt/go/pkg/mod/github.com/cockroachdb/pebble/v2@v2.1.4/commit.go')
write('source-bindings.json',{'revision':'4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac','files':[{'path':n,'bytes':(S/n).stat().st_size,'sha256':H(S/n)} for n in paths],'absent_FLOW_files':['internal/runtime/persondirectory/FLOW.md','pkg/raftlog/FLOW.md'],'external_pinned_source':{'module':'github.com/cockroachdb/pebble/v2','version':'v2.1.4','file':'commit.go','bytes':external.stat().st_size,'sha256':H(external)},'definitions':{'ReadBarrier96':'one select waiting request.resp or ctx.Done; cannot distinguish queue/quorum/apply from that frame','DBsubmitResult329':'one select waiting pending.done/cancel/close, not necessarily physical disk write','raftlogSave186':'submitWrite90 waits req.done from shared one writer;12Save not12physicalfsync','raftlogCollect132':'one5ms timer set at first dequeue; not reset on each arrival; snapshotselect/runnable alone cannot establish delay duration','raftlogFlush184':'Pebble Batch.Commit(Sync); publish492 may wait another publisher orWALsync, not uniquely fsync','persondirWorker397':'idle task select','persondirWorker406':'projectBatch; membership485 andcompletion515 remain separate stages','runnable_state':'point state, not a measured scheduling delay or time duration'}})
print(json.dumps({'rows':len(rows),'goroutine_total':sum(r['snapshotted_goroutines'] for r in rows),'input_bytes':totalbytes,'elapsed_seconds':time.monotonic()-started}))
