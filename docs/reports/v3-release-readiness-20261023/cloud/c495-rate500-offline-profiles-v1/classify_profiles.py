from pathlib import Path
import collections,json,runpy
O=Path(__file__).resolve().parent
ns=runpy.run_path(str(O/'decode_profiles.py'),run_name='offline_profile_decoder')
meta,stacks=ns['load']('goroutine')
cpu,cpu_stacks=ns['load']('cpu')
PREFIX='github.com/WuKongIM/WuKongIM/'
def has(r,s):return any(f['function']==PREFIX+s for f in r['frames'])
def line(r,s,n):return any(f['function']==PREFIX+s and f['line']==n for f in r['frames'])
def appfirst(r):
 for f in r['frames']:
  if f['function'].startswith(PREFIX) and not any(t in f['function'] for t in ['/pkg/transport','/pkg/cluster/net','/pkg/cluster.(*Node).CallRPC','/pkg/cluster.(*Node).RPCService']):return f['function'].removeprefix(PREFIX),f['line']
 return 'other',0
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
assert sum(worker.values())==320 and worker['unclassified']==0
assert sum(recovery.values())==12 and recovery['unclassified']==0
assert sum(owner_context.values())==4 and owner_context['other']==0
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
(O/'aggregate-counts.json').write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps({k:summary[k] for k in ['goroutines_total','delivery_runWorker_disjoint','recoverPage_disjoint','actual_owner_read_holder_context_disjoint','actual_owner_read_operation_disjoint','Conn_Call_total','delivery_enqueue_callers','Slot_runApplyTask_callers','cpu_sample_count','cpu_total_sample_nanoseconds']},indent=2))
