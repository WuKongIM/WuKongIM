#!/usr/bin/env python3
import pathlib,json,re,hashlib,collections
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1030-ci-generation-1')
HEAD='56c287493a5e9fc3df65ea0631b99612672cd678';BASE='27fa6d63cf3b2b6c3a9a28575f917d60cc054ded';MERGE='a514c73b4c950b9ead150c28ff08ef89123f5238'
P=sorted((R/'polls').iterdir())[-1]
artifacts=[a for p in P.glob('artifacts-*.json') for a in json.loads(p.read_text())['artifacts']]
policy_body=(R/'frozen-source'/BASE/'.github/review-agent/policy.json').read_bytes();policy=json.loads(policy_body)
sha=lambda b:hashlib.sha256(b).hexdigest()
named={};performance={}
for a in artifacts:
 d=R/'verified-artifacts'/str(a['id'])
 if a['name'].startswith(('v3-candidate-default-','v3-candidate-scripts-')):
  name='go-unit' if a['name'].startswith('v3-candidate-default-') else 'scripts-integration'
  identity=json.loads((d/'identity.json').read_text());command=json.loads((d/'command.json').read_text());result=json.loads((d/'result.json').read_text());frozen=policy['trusted_checks'][name]
  assert identity['source_sha']==MERGE and identity['policy_sha']==BASE and identity['policy_sha256']==sha(policy_body) and identity['run_attempt']=='1'
  assert command['named_check']==name and {k:command[k] for k in frozen}==frozen
  assert command['environment']==({'GOWORK':'off','GOFLAGS':'-p=2'} if name=='go-unit' else {'GOWORK':'off','GOFLAGS':'-p=2 -json'})
  assert result['passed'] is True and result['status']=='passed' and result['exit_code']==0 and result['wrapper_exit_code']==0
  assert result['output_truncated'] is False and result['elapsed_seconds']<frozen['timeout_seconds'] and result['output_bytes']<frozen['max_output_bytes']
  assert result['cleanup']['verified'] is True and result['cleanup']['complete'] is True and result['cleanup']['remaining_processes']==[]
  assert (d/'policy.json').read_bytes()==policy_body
  named[name]={'artifact_id':a['id'],'identity':identity,'command':command,'result':result}
  body=(d/'stdout-stderr.log').read_text()
  if name=='go-unit':
   summary={'passed':True,'elapsed_seconds':result['elapsed_seconds'],'output_bytes':result['output_bytes'],'package_pass_count':len(re.findall(r'^ok\s',body,re.M)),'no_test_file_summaries':len(re.findall(r'^\?\s',body,re.M)),'changed_package_summaries':[line for line in body.splitlines() if any(x in line for x in ['github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/benchmark','github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle','github.com/WuKongIM/WuKongIM/internal/bench/target'])],'individual_test_case_counts_unavailable_nonverbose':True}
   assert len(summary['changed_package_summaries'])==3 and all(line.startswith('ok') for line in summary['changed_package_summaries'])
   (R/'default-suite-log-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
  else:
   events=[json.loads(l) for l in body.splitlines() if l.startswith('{')]
   test=[e for e in events if 'Test' in e];pkgs=[e for e in events if 'Test' not in e]
   fails=[e for e in events if e.get('Action')=='fail'];assert not fails
   skipped=[e for e in test if e.get('Action')=='skip'];skipnames={e['Test'] for e in skipped}
   summary={'test_event_counts':dict(collections.Counter(e['Action'] for e in test)),'package_event_counts':dict(collections.Counter(e['Action'] for e in pkgs)),'failed_tests':fails,'skips':skipped,'skip_reasons':[e for e in events if e.get('Action')=='output' and e.get('Test') in skipnames],'output_truncated':False,'elapsed_seconds':result['elapsed_seconds'],'output_bytes':result['output_bytes'],'cleanup':result['cleanup']}
   (R/'scripts-events-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
 if a['name'].startswith('500qps-'):
  key=next(k for k in ['channel-append','mixed-send','tcp-sendack'] if k in a['name'])
  arrival=json.loads((d/'arrival.json').read_text());warmup=json.loads((d/'arrival.warmup.json').read_text())
  assert arrival['schema']==warmup['schema']=='scheduled-arrival/v1' and arrival['phase']=='measurement' and arrival['qualification'] is True and warmup['phase']=='warmup' and warmup['qualification'] is False
  assert len(arrival['windows'])==3 and len(arrival['seconds'])==180 and len(warmup['windows'])==1 and len(warmup['seconds'])==60
  for x in arrival['windows']+warmup['windows']:
   assert x['planned']==x['started']==x['completed']==30000 and x['errors']==x['dropped']==0 and x['target_per_second']==500 and x['started_per_second']>=475 and x['scheduled_to_completion_p99_ms']<=400
  assert json.loads((d/'result.json').read_text())=={'status':'pass'} and (d/'source.sha').read_text().strip()==MERGE
  assert re.fullmatch(r'[0-9a-f]{64}',(d/'binary.sha256').read_text().split()[0])
  assert (d/'go-version.txt').read_text().strip()=='go version go1.25.11 linux/amd64'
  host=json.loads((d/'host.json').read_text());cpu={x['field']:x['data'] for x in host['lscpu']};assert cpu['CPU(s):']=='4' and cpu['Architecture:']=='x86_64'
  perf={'artifact_id':a['id'],'schema':arrival['schema'],'phase':arrival['phase'],'qualification':True,'windows':arrival['windows'],'one_second_cohorts':len(arrival['seconds']),'warmup_windows':warmup['windows'],'source_sha':MERGE,'binary_sha256_file':(d/'binary.sha256').read_text(),'go_version':(d/'go-version.txt').read_text().strip(),'visible_CPUs':cpu['CPU(s):'],'CPU_model':cpu['Model name:'],'data_filesystem':json.loads((d/'data-filesystem.json').read_text())}
  if key in ['channel-append','mixed-send']:
   c=json.loads((d/'counters/window.json').read_text());before=json.loads((d/'counters/before.json').read_text());after=json.loads((d/'counters/after.json').read_text())
   assert c['schema']==key+'-counters/v1' and c['operations']==90000 and c['offered_qps']==500 and c['handlers_completed'] is True and c['gomaxprocs']==4
   assert c['started_at']==before['at'] and c['ended_at']==after['at']
   perf['counter_window']=c
  performance[key]=perf
(R/'named-check-validation.json').write_text(json.dumps(named,indent=2)+'\n')
(R/'performance-validation.json').write_text(json.dumps(performance,indent=2)+'\n')
print(json.dumps({'validated_named_checks':list(named),'validated_performance_seams':{k:[w['scheduled_to_completion_p99_ms'] for w in v['windows']] for k,v in performance.items()}}))
