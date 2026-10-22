from pathlib import Path
import runpy,contextlib,io,json,collections
O=Path('/tmp/wukongim-v3-readiness-20261023/cloud/exact79a-profile-signals-v1')
with contextlib.redirect_stdout(io.StringIO()):ns=runpy.run_path(str(O/'decode_selected.py'))
meta,stacks=ns['load']('goroutine')
PREFIX='github.com/WuKongIM/WuKongIM/'
def has(r,s):return any(f['function']==PREFIX+s for f in r['frames'])
def line(r,s,n):return any(f['function']==PREFIX+s and f['line']==n for f in r['frames'])
def appfirst(r):
 for f in r['frames']:
  if f['function'].startswith(PREFIX) and not any(t in f['function'] for t in ['/pkg/transport','/pkg/cluster/net','/pkg/cluster.(*Node).CallRPC','/pkg/cluster.(*Node).RPCService']):return f['function'].removeprefix(PREFIX),f['line']
 return 'other',0
workers=collections.Counter();rpc=collections.Counter();sampled=[];queue=collections.Counter();apply=[]
for r in stacks:
 n=r['values'][0]
 if has(r,'internal/runtime/delivery.(*Runtime).runWorker'):
  if line(r,'internal/usecase/presence.(*AuthorityRecovery).recoverPage',132):k='presence_cold_lane_select'
  elif has(r,'internal/infra/cluster.(*PresenceRecoveryOwners).RecoverRoutesByTargets'):k='presence_owner_recovery_RPC'
  elif has(r,'internal/access/node.(*Client).EndpointsByTargets'):k='presence_remote_authority_RPC'
  elif any(f['function'].endswith('sync.(*WaitGroup).Wait') for f in r['frames']) and has(r,'internal/infra/cluster.runBoundedPresenceLeaderBatches'):k='presence_parallel_leader_join'
  elif line(r,'internal/runtime/delivery.(*Runtime).processPlan',595):k='owner_push_parallel_join_or_inline'
  else:k='other:'+str(appfirst(r))
  workers[k]+=n;sampled.append({'group':'delivery_runWorker','category':k,'count':n,'frames':r['frames']})
 if has(r,'pkg/transport/internal/conn.(*Conn).Call'):
  k=appfirst(r);rpc[k]+=n
 if has(r,'internal/runtime/delivery.(*orderedPlanQueue).enqueue'):
  k='global_plan_queue_capacity_select_line81' if line(r,'internal/runtime/delivery.(*orderedPlanQueue).enqueue',81) else 'other';queue[k]+=n
 if has(r,'pkg/slot/multiraft.(*slot).runApplyTask'):apply.append({'count':n,'frames':r['frames']})
x={'profile_time':meta['time_utc'],'delivery_runWorker_disjoint':dict(workers),'delivery_runWorker_total':sum(workers.values()),'Conn_Call_disjoint_nearest_non_transport_product_frame':[{'function':k,'line':ln,'count':n} for (k,ln),n in rpc.items()],'Conn_Call_total':sum(rpc.values()),'enqueue_disjoint':dict(queue),'slot_apply_executing_stacks':apply,'limits':'Partitions are disjoint only within their own cohort. Cohorts overlap each other; one-node one-point counts, not durations. Nearest product frame classifies RPC caller, not remote serving wait.'}
assert sum(workers.values())==320 and sum(rpc.values())==664 and sum(queue.values())==414
(O/'disjoint-counts.json').write_text(json.dumps(x,indent=2)+'\n');(O/'delivery-worker-selected-stacks.json').write_text(json.dumps(sampled,indent=2)+'\n')
print(json.dumps({k:v for k,v in x.items() if k!='slot_apply_executing_stacks'},indent=2))
