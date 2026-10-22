from pathlib import Path
import json,re,collections,hashlib
P=Path('/tmp/wukongim-v3-readiness-20261023/cloud/full-original-distribution-exact79a-profile-v1');O=P.parent/'exact79a-profile-signals-v1'
labels={'node_id','node_name','kind','stage','result','slot_id','store','phase','reason','component','pool','priority','queue','scope','stat','quantile'}
def keep(n):
 return (n.startswith('wukongim_storage_commit_') or any(n.startswith(k) for k in ['wukongim_storage_pebble_wal_fsync_','wukongim_storage_pebble_write_stall','wukongim_message_permission_','wukongim_slot_read_barrier_','wukongim_slot_apply_','wukongim_delivery_recipient_worker_']) or n in ['wukongim_runtime_pool_queue_depth','wukongim_runtime_pool_queue_capacity','go_sched_gomaxprocs_threads']) and not n.endswith('_bucket')
cut={}
for node in [1,2,3]:
 for stage in ['first-measured','terminal']:
  rows=[]
  for line in (P/f'evidence/metrics-{stage}-node{node}.prom').read_text().splitlines():
   m=re.fullmatch(r'([a-zA-Z_:][a-zA-Z0-9_:]*)(\{.*\})?\s+(\S+)',line)
   if not m or not keep(m[1]):continue
   d={k:v for k,v in re.findall(r'([a-zA-Z_][a-zA-Z0-9_]*)="([^"\\]*)"',m[2] or '') if k in labels}
   rows.append({'name':m[1],'labels':d,'value':float(m[3])})
  cut[node,stage]=rows
summary={'metric_cuts':[{'node':n,'cut':s,'rows':r} for (n,s),r in cut.items()],'between_cut_means':[],'storage_deltas':[],'slot_gaps':[],'limitations':'Sequential retained cuts do not establish exact GOR-time values. Duration sample groups overlap; no decomposition of barrier into quorum/control-queue/durable apply is available.'}
def ident(r):return r['name'],tuple(sorted(r['labels'].items()))
for n in [1,2,3]:
 a={ident(r):r for r in cut[n,'first-measured']};b={ident(r):r for r in cut[n,'terminal']}
 for key,last in b.items():
  first=a.get(key)
  if first is None:continue
  name=last['name']
  if name.endswith('_count'):
   sumkey=(name[:-6]+'_sum',key[1]);aa=a.get(sumkey);bb=b.get(sumkey)
   if aa is not None and bb is not None:
    count=last['value']-first['value'];total=bb['value']-aa['value']
    if count>0 and total>=0:summary['between_cut_means'].append({'node':n,'name':name[:-6],'labels':last['labels'],'count':count,'total_seconds':total,'mean_ms':total/count*1000})
  if any(name.startswith(k) for k in ['wukongim_storage_pebble_wal_fsync_','wukongim_storage_pebble_write_stall']):summary['storage_deltas'].append({'node':n,'name':name,'labels':last['labels'],'first':first['value'],'terminal':last['value'],'delta':last['value']-first['value']})
  if name=='wukongim_slot_apply_gap':summary['slot_gaps'].append({'node':n,'labels':last['labels'],'first':first['value'],'terminal':last['value']})
(O/'metrics-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
print('permission barrier/snapshot means')
for r in summary['between_cut_means']:
 if r['name']=='wukongim_message_permission_duration_seconds' and r['labels'].get('stage') in ['barrier','snapshot']:print(r['node'],r['labels'],round(r['mean_ms'],3),r['count'])
print('storage batch stages (node2)')
for r in summary['between_cut_means']:
 if r['node']==2 and 'storage_commit' in r['name'] and 'duration' in r['name']:print(r)
print('wal/write stall (node2)')
for r in summary['storage_deltas']:
 if r['node']==2 and (r['delta']!=0 or r['terminal']!=0):print(r)
print('slot gaps nonzero',[r for r in summary['slot_gaps'] if r['first'] or r['terminal']])
