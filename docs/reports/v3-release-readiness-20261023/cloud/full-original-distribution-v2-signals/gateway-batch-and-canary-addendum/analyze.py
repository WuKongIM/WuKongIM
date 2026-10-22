from pathlib import Path
import json,re,hashlib,base64,collections,subprocess
P=Path('/tmp/wukongim-v3-readiness-20261023/cloud/full-original-distribution-v2')
C=Path('/tmp/wukongim-v3-readiness-20261021/cloud/pr1024-authorized-run-resume-20261023/status-check-20261023/public-v1')
O=P.parent/'full-original-distribution-v2-signals/gateway-batch-and-canary-addendum'
S=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis')
result={'local_actual_batches':[],'cloud_batch_coverage':[],'canary':{},'input_bindings':[]}
def bind(p):result['input_bindings'].append({'path':str(p),'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()})
for n in [1,2,3]:
 for cut in ['first-measured','terminal']:
  f=P/f'evidence/metrics-{cut}-node{n}.prom';bind(f);batch={};cohort={}
  for line in f.read_text().splitlines():
   if line.startswith('#') or not line:continue
   name=line.split('{',1)[0].split(' ',1)[0]
   if name.startswith('wukongim_gateway_async_send_batch_records'):
    if name.endswith('_bucket'):
     le=re.search(r'le="([^"]+)"',line).group(1);batch['le_'+le]=float(line.rsplit(' ',1)[1])
    else:batch['sum' if name.endswith('_sum') else 'count']=float(line.rsplit(' ',1)[1])
   elif name=='wukongim_message_permission_counts_total':
    kind=re.search(r'kind="([^"]+)"',line).group(1)
    if kind in ['cohort_requests','cohorts','cohort_facts','cohort_busy']:cohort[kind]=float(line.rsplit(' ',1)[1])
  assert batch['sum']==batch['count']==batch['le_1']
  result['local_actual_batches'].append({'node':n,'cut':cut,'batch_records':batch,'actual_mean_batch_records':batch['sum']/batch['count'],'all_observed_batches_le_one':True,'permission_cohort_counters':cohort,'accepted_requests_per_cohort':cohort['cohort_requests']/cohort['cohorts'],'facts_per_cohort':cohort['cohort_facts']/cohort['cohorts']})
manifest=C/'terminal/evidence/manifest.json';bind(manifest);capture=json.loads(manifest.read_text())['captures']
for family in ['presence','delivery','up','runtime','channel_worker','process','counters','go']:
 f=C/f'terminal/evidence/metrics/range-{family}.json';bind(f);rows=json.loads(f.read_text())['data']['result'];found=sum('gateway_async_send_batch_records' in row['metric'].get('__name__','') or 'message_permission_counts_total' in row['metric'].get('__name__','') for row in rows)
 result['cloud_batch_coverage'].append({'partition':family,'query':next(x['query'] for x in capture if x['name']=='range-'+family),'total_series':len(rows),'gateway_batch_or_permission_cohort_series':found})
assert sum(x['gateway_batch_or_permission_cohort_series'] for x in result['cloud_batch_coverage'])==0
final=P/'report/final.json';bind(final);f=json.loads(final.read_text())
worker=P/'report/worker-evidence.json';bind(worker);run=json.loads(worker.read_text())['run_id'];raw=run.encode();root=hashlib.sha256(b'wukongim/chat-lifecycle/run-key/v1'+len(raw).to_bytes(8,'big')+raw+(1).to_bytes(8,'big')).digest();ns=base64.b32encode(hashlib.sha256(b'identity-namespace/v1'+root).digest()[:10]).decode().rstrip('=').lower();prefix='wku-'+ns+'-'
rosters=collections.Counter();phases=collections.Counter();matches=[]
for n in [1,2,3]:
 log=P/f'logs/service-{n}.log';bind(log)
 for line in log.read_text().splitlines():
  m=re.search(r'\{.*\}',line)
  if not m:continue
  try:d=json.loads(m[0])
  except:continue
  if d.get('event')!='internal.app.delivery.plan_incomplete':continue
  # app ObservePlanTerminal logs Failure.RecipientUID, never the sender identity.
  uid=d.get('uid','')
  if not uid.startswith(prefix):rosters['unknown_namespace']+=1;continue
  index=int(uid[len(prefix):],36)
  name='very_large_fixed_roster' if index%2000==1999 else 'other_fixed_roster'
  rosters[name]+=1;phases[d.get('phase','unknown')]+=1
  if name=='very_large_fixed_roster':matches.append({'node':n,'phase':d.get('phase'),'at':line[:23]})
identity=P/'identity-helper-summary.json';bind(identity);decoded=json.loads(identity.read_text())['results'];domain_counts=collections.Counter(r['decoded']['domain_name'] for r in decoded)
result['canary']={'configured_group_count':1,'configured_fixed_members':100000,'configured_cadence_seconds':60,'local_final_primary_generated':f['generated']['primary'],'local_final_canary_generated':f['generated']['canary'],'local_final_logical_sent':f['messages']['sent'],'local_final_logical_acknowledged':f['messages']['send_acknowledged'],'aggregate_canary_success_inference':'primary12000+canary1=sent=ACK12001; no per-canary packet timestamp retained','canary_clock_source':'TrafficGenerator constructor/reset anchors nextCanary to engine-generation start + cadence, not explicitly to final.report.window.start; exact emitted/ACK/recipient-delivery time is missing','first_canary_send_time':None,'first_canary_ack_time':None,'canary_presence_cold_reconstruction_duration':None,'expired_sample_decoded_domains':dict(domain_counts),'delivery_failure_logged_field':'Failure.RecipientUID (not sender or channel identity)','delivery_failure_recipient_roster_counts':dict(rosters),'delivery_failure_phases':dict(phases),'very_large_roster_failure_examples_sanitized':matches,'interpretation':'No logged incomplete-plan RecipientUID belongs to fixed very-large roster. This weakens direct-canary-timeout attribution, but cannot exclude indirect fanout/cache/route pressure. Roster membership is not a per-message causal join.'}
source=['AGENTS.md','internal/bench/FLOW.md','internal/bench/chatlifecycle/FLOW.md','internal/app/FLOW.md','internal/bench/chatlifecycle/traffic.go','internal/bench/chatlifecycle/groups.go','internal/bench/chatlifecycle/identity.go','internal/app/online_delivery.go']
result['source_sha256']={}
for path in source:
 blob=subprocess.check_output(['git','show','HEAD:'+path],cwd=S);assert blob==(S/path).read_bytes();result['source_sha256'][path]=hashlib.sha256(blob).hexdigest()
result['scope']={'source_revision':'79a7177032a6ae1ec7015cefe0e6f94eb0f19f06','offline_only':True,'product_or_profile_or_network_started':False,'original_signals_manifest_unchanged':True,'raw_identities_or_logs_copied_publicly':False}
(O/'summary.json').write_text(json.dumps(result,indent=2)+'\n')
print('bounded addendum:',len(result['local_actual_batches']),'local batch cuts;',len(result['cloud_batch_coverage']),'cloud partitions; fixed-roster classifications:',dict(rosters))
