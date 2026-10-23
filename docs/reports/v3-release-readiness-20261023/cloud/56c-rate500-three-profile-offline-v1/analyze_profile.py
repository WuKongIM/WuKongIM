import collections,datetime,hashlib,json,pathlib,sys,time
import offline_adapter as A
ROOT=pathlib.Path('/tmp/wukongim-v3-readiness-20261023');OUT=ROOT/'cloud/56c-rate500-three-profile-offline-v1';PRIVATE=ROOT/'private/56c-rate500-three-profile-offline-v1';APPROVAL=ROOT/'cloud/56c-three-profile-offline-root-approval-v1/approval.json';a=json.loads(APPROVAL.read_text());row=a['inputs'][int(sys.argv[1])];node=row['node_id'];kind=row['kind'];stem=f'node-{node}-{kind}';receipt={'node_id':node,'kind':kind,'started_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat()};start=time.monotonic()
try:
 receipt['input_before']=A.verify_input(row,A.fixed_paths(ROOT/'cloud/full-original-distribution-56c-rate500-three-profile-v1'))
 meta,stacks=A.load_profile(row['path']);A.verify_types(kind,meta,stacks)
 raw=A.bounded_json({'metadata':meta,'stacks':stacks},8388608)
 with (PRIVATE/f'{stem}-symbol-stacks.json').open('xb') as f:f.write(raw)
 (PRIVATE/f'{stem}-symbol-stacks.json').chmod(0o600)
 try:summary=A.classify_goroutine(meta,stacks) if kind=='goroutine' else A.classify_cpu(meta,stacks);receipt['classification_status']='complete'
 except Exception as error:summary={'profile':meta};receipt['classification_status']='partial';receipt['classification_error_class']=A.closed_error(error)
 if kind=='goroutine':
  counts=collections.Counter();lock_context=collections.Counter()
  for r in stacks:
   n=r['values'][0]
   for name in set(f['function'] for f in r['frames']):
    if any(t in name for t in ['ReadOwner','readOwner','Registry','registry','recoverPage','admitRecovery','executeRecovery','RWMutex','Mutex']):counts[name]+=n
   if any('Mutex' in f['function'] for f in r['frames']):
    frames=tuple((f['function'],f['line']) for f in r['frames'] if f['function'].startswith('github.com/WuKongIM/WuKongIM/'))
    lock_context[frames]+=n
  summary['selected_symbol_counts_overlapping']=[{'function':fn,'count':n} for fn,n in counts.most_common()]
  private_contexts=[{'count':n,'frames':[{'function':fn,'line':ln} for fn,ln in fs]} for fs,n in lock_context.most_common()]
  context_path=PRIVATE/f'node-{node}-mutex-contexts-private.json'
  with context_path.open('x') as f:f.write(json.dumps(private_contexts,indent=2)+'\n')
  context_path.chmod(0o600)
  nearest=collections.Counter()
  for fs,n in lock_context.items():
   if fs:nearest[fs[0]]+=n
  summary['mutex_nearest_product_symbol_line_disjoint']=[{'function':fn,'line':ln,'count':n} for (fn,ln),n in nearest.items()]
 summary['node_id']=node;summary['capture_metadata_binding']=row['node_metadata_sha256'];summary['request_status']=row['request_status'];summary['first_grant']=a['first_grant'];summary['terminal_supervisor_at']=a['terminal_supervisor_at'];summary['active_SEND_end_unknown']=True
 data=A.bounded_json(summary,65536)
 with (OUT/f'{stem}-aggregate.json').open('xb') as f:f.write(data)
 receipt['aggregate_sha256']=hashlib.sha256(data).hexdigest();receipt['aggregate_bytes']=len(data);receipt['private_decoded_bytes']=len(raw);receipt['input_after']=A.verify_input(row,A.fixed_paths(ROOT/'cloud/full-original-distribution-56c-rate500-three-profile-v1'));receipt['status']='complete'
except Exception as error:receipt['status']='partial';receipt['error_class']=A.closed_error(error)
receipt['completed_at_utc']=datetime.datetime.now(datetime.timezone.utc).isoformat();receipt['elapsed_seconds']=time.monotonic()-start
with (OUT/f'{stem}-decode-receipt.json').open('x') as f:f.write(json.dumps(receipt,indent=2)+'\n')
print(json.dumps({'node_id':node,'kind':kind,'status':receipt['status'],'classification_status':receipt.get('classification_status'),'error_class':receipt.get('error_class'),'classification_error_class':receipt.get('classification_error_class'),'elapsed_seconds':receipt['elapsed_seconds']}))
