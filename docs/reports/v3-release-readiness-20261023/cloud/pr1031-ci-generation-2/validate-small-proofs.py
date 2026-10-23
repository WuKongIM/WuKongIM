from pathlib import Path
import json,re,hashlib,collections,zipfile,datetime
B=Path(__file__).parent;P=B.with_name(B.name+'-private');H='2662fe93b754d614422f2936185cf781a33b831a';BASE='8dfcd35b7d604fb5d3da3d75e36ab267e2f4b1bf';M='5b3fef1c7a3418c13c4a4392533f112915d97ffe';RUN='39879047480'
sha=lambda b:hashlib.sha256(b).hexdigest()
policy_body=(P/'policy.json').read_bytes();policy=json.loads(policy_body)['trusted_checks'];named={};perf={}
def save(name,v):
 target=B/name
 if target.exists():assert target.read_bytes()==(json.dumps(v,indent=2)+'\n').encode();return
 target.write_text(json.dumps(v,indent=2)+'\n')
def events(body):return [json.loads(x) for x in body.decode().splitlines() if x.startswith('{')]
def safe_event(e):return {k:e[k] for k in ['Time','Action','Package','Test','Elapsed'] if k in e}
def verify_result(r,limit,maxbytes):
 assert r['passed'] is True and r['status']=='passed' and r['exit_code']==0 and r['wrapper_exit_code']==0 and r['output_truncated'] is False
 assert r['elapsed_seconds']<limit and r['output_bytes']<maxbytes
 assert r['cleanup']['verified'] is True and r['cleanup']['complete'] is True and r['cleanup']['remaining_processes']==[]
for receipt in sorted((B/'proof-inventory').glob('*.json')):
 a=json.loads(receipt.read_text())
 if a.get('state')!='verified_private_original':continue
 z=zipfile.ZipFile(P/'archives'/(str(a['id'])+'.zip'));read=lambda n:json.loads(z.read(n))
 if a['name'].startswith(('v3-candidate-default-','v3-candidate-scripts-')):
  n='go-unit' if a['name'].startswith('v3-candidate-default-') else 'scripts-integration';ident=read('identity.json');cmd=read('command.json');r=read('result.json');frozen=policy[n]
  assert ident['source_sha']==M and ident['policy_sha']==BASE and ident['policy_sha256']==sha(policy_body) and ident['run_id']==RUN and ident['run_attempt']=='1'
  assert cmd['named_check']==n and {k:cmd[k] for k in frozen}==frozen
  assert cmd['environment']==({'GOWORK':'off','GOFLAGS':'-p=2'} if n=='go-unit' else {'GOWORK':'off','GOFLAGS':'-p=2 -json'})
  assert z.read('policy.json')==policy_body
  # Preserve genuine result before interpreting any success predicate.
  result={'artifact_id':a['id'],'identity':ident,'command':cmd,'result':r}
  save(n+'-result.json',result);verify_result(r,frozen['timeout_seconds'],frozen['max_output_bytes']);named[n]=result
  log=z.read('stdout-stderr.log')
  if n=='go-unit':
   text=log.decode();summary={'passed':True,'package_pass_count':len(re.findall(r'^ok\s',text,re.M)),'no_test_file_summaries':len(re.findall(r'^\?\s',text,re.M)),'elapsed_seconds':r['elapsed_seconds'],'output_bytes':r['output_bytes'],'individual_test_case_counts_unavailable_nonverbose':True,'source_log_sha256':sha(log),'package_summaries':[line for line in text.splitlines() if line.startswith(('ok\t','?\t','ok ','? '))]};save('default-summary.json',summary)
  else:
   es=events(log);test=[e for e in es if 'Test'in e];roots=[e for e in test if '/' not in e['Test']];package=[e for e in es if 'Test' not in e]
   failed=[safe_event(e) for e in es if e.get('Action')=='fail'];started={(e.get('Package'),e['Test']) for e in test if e.get('Action')=='run'};finished={(e.get('Package'),e['Test']) for e in test if e.get('Action') in ['pass','skip','fail']}
   summary={'root_counts':dict(collections.Counter(e['Action'] for e in roots)),'all_test_counts':dict(collections.Counter(e['Action'] for e in test)),'package_counts':dict(collections.Counter(e['Action'] for e in package)),'failed':failed,'unfinished':[{'Package':p,'Test':t} for p,t in sorted(started-finished)],'skips':[safe_event(e) for e in test if e.get('Action')=='skip'],'elapsed_seconds':r['elapsed_seconds'],'output_bytes':r['output_bytes'],'output_truncated':False,'source_log_sha256':sha(log),'raw_Output_lines_private':True,'test_name_filter_applied':False}
   save('scripts-summary.json',summary);save('scripts-safe-events.json',[safe_event(e) for e in es if e.get('Action')!='output']);assert not failed and not summary['unfinished']
 if a['name'].startswith('500qps-'):
  key=next(k for k in ['tcp-sendack','channel-append','mixed-send'] if k in a['name']);v=read('arrival.json');warm=read('arrival.warmup.json')
  assert v['schema']==warm['schema']=='scheduled-arrival/v1' and v['phase']=='measurement' and v['qualification'] is True and warm['phase']=='warmup' and warm['qualification'] is False
  assert len(v['windows'])==3 and len(v['seconds'])==180 and len(warm['windows'])==1 and len(warm['seconds'])==60
  for w in v['windows']+warm['windows']:
   assert w['planned']==w['started']==w['completed']==30000 and w['errors']==w['dropped']==0 and w['target_per_second']==500 and w['started_per_second']>=475 and w['scheduled_to_completion_p99_ms']<=400
  assert read('result.json')=={'status':'pass'} and z.read('source.sha').decode().strip()==M
  binary=z.read('binary.sha256').decode().split()[0];assert re.fullmatch('[0-9a-f]{64}',binary)
  gv=z.read('go-version.txt').decode().strip();assert gv=='go version go1.25.11 linux/amd64'
  cpu={x['field']:x['data'] for x in read('host.json')['lscpu']};assert cpu['CPU(s):']=='4' and cpu['Architecture:']=='x86_64'
  item={'artifact_id':a['id'],'source_sha':M,'binary_sha256':binary,'go_version':gv,'visible_cpus':cpu['CPU(s):'],'cpu_model':cpu['Model name:'],'measurement_windows':v['windows'],'warmup_windows':warm['windows'],'measurement_second_cohorts':len(v['seconds']),'warmup_second_cohorts':len(warm['seconds']),'data_filesystem':read('data-filesystem.json'),'result':read('result.json')}
  if key!='tcp-sendack':
   c=read('counters/window.json');before=read('counters/before.json');after=read('counters/after.json');assert c['schema']==key+'-counters/v1' and c['operations']==90000 and c['offered_qps']==500 and c['handlers_completed'] is True and c['gomaxprocs']==4 and c['started_at']==before['at'] and c['ended_at']==after['at'];item['counters_window']=c
  save('performance-'+key+'.json',item);perf[key]=item
 if a['name'].startswith('v3-candidate-e2e-'):
  expected={'binary-build-info.txt','binary.sha256','go-version.txt','identity.json'}|{f'{c}/{n}' for c in ['build','ordinary_history','cross_node_delivery','self_chat'] for n in ['command.json','result.json','stderr.log','stdout.jsonl']}|{'self_chat/report.json'};assert set(z.namelist())==expected
  ident=read('identity.json');assert ident['source_sha']==M and ident['policy_sha']==BASE and ident['run_id']==RUN and ident['run_attempt']=='1'
  binary=z.read('binary.sha256').decode().split()[0];assert re.fullmatch('[0-9a-f]{64}',binary)
  bi=z.read('binary-build-info.txt').decode();assert 'go1.25.11' in bi and 'vcs.revision='+M in bi and 'vcs.modified=false' in bi
  commands={'build':(720,['go','build','-tags=e2e','-o',f'/home/runner/work/_temp/v3-candidate-{RUN}-1/e2e/wukongim-e2e','./cmd/wukongim']),'ordinary_history':(780,['go','test','-tags=e2e','./test/e2e/migration/ordinary_history','-count=1','-timeout=12m','-p=1','-parallel=1','-json']),'cross_node_delivery':(180,['go','test','-tags=e2e','./test/e2e/message/cross_node_delivery','-count=1','-timeout=2m','-p=1','-json']),'self_chat':(300,['go','test','-tags=e2e','./test/e2e/message/self_chat','-count=1','-timeout=4m','-p=1','-json'])}
  item={'artifact_id':a['id'],'identity':ident,'binary_sha256':binary,'embedded_vcs_revision':M,'embedded_vcs_modified':False,'cases':{}}
  for case,(limit,args) in commands.items():
   cmd=read(case+'/command.json');r=read(case+'/result.json');assert cmd['arguments']==args and cmd['timeout_seconds']==limit and cmd['max_output_bytes']==64<<20 and cmd['working_dir']=='/home/runner/work/WuKongIM/WuKongIM';verify_result(r,limit,64<<20)
   es=events(z.read(case+'/stdout.jsonl'));failed=[safe_event(e) for e in es if e.get('Action')=='fail'];assert not failed
   item['cases'][case]={'command':cmd,'result':r,'test_pass_events':[safe_event(e) for e in es if e.get('Action')=='pass' and 'Test'in e],'skip_events':[safe_event(e) for e in es if e.get('Action')=='skip'],'fail_events':failed}
  assert [len(item['cases'][k]['test_pass_events']) for k in ['ordinary_history','cross_node_delivery','self_chat']]==[6,4,3]
  skips=[e for v in item['cases'].values() for e in v['skip_events']];assert len(skips)==1 and skips[0]['Test']=='TestThreeNodeReplicaLoadComparisonAtTwoThousandQPS'
  report=read('self_chat/report.json');assert report['passed'] is True and report['hash_slots']==256 and report['token_authentication']=='production_default' and {c['nodes'] for c in report['cases']}=={1,3}
  safe={'passed':report['passed'],'hash_slots':report['hash_slots'],'token_authentication':report['token_authentication'],'suppression_window_ms':report['suppression_window_ms'],'cases':[]}
  for c in report['cases']:
   assert c['passed'] is True and c['binary_sha256']==binary and c['invalid_auth_refused'] is True and c['whole_cluster_restarts']==1
   safe['cases'].append({k:c[k] for k in ['nodes','passed','binary_sha256','invalid_auth_refused','whole_cluster_restarts','sender_suppression_windows']}|{'acknowledged_count':len(c['acknowledged']),'receive_count':len(c['receives']),'history_count':len(c['history'])})
  item['self_chat']=safe;item['raw_identity_report_private']=True
  save('e2e-summary.json',item)
print(json.dumps({'named_checks':list(named),'performance_seams':{k:[w['scheduled_to_completion_p99_ms'] for w in v['measurement_windows']] for k,v in perf.items()},'e2e_summary_written':(B/'e2e-summary.json').exists()}))
