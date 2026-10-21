from pathlib import Path
import json,zipfile,collections,hashlib
O=Path(__file__).resolve().parent;P=O.parent/'candidate-private';snap=O/(O/'latest-snapshot.txt').read_text().strip()
read=lambda p:json.loads(p.read_text())
a=next(a for a in read(snap/'artifacts.json')['artifacts'] if a['name'].startswith('v3-candidate-scripts-'))
M=O/'artifacts-public'/str(a['id'])/'members'
with zipfile.ZipFile(P/(str(a['id'])+'.zip')) as z:raw=z.read('stdout-stderr.log')
events=[]
for line in raw.splitlines():
 try:v=json.loads(line)
 except (json.JSONDecodeError,UnicodeDecodeError):continue
 if isinstance(v,dict) and v.get('Action') in ['start','run','pause','cont','pass','fail','skip']:
  events.append({k:v[k] for k in ['Time','Action','Package','Test','Elapsed'] if k in v})
roots={}
for e in events:
 if e.get('Test') and '/' not in e['Test']:roots.setdefault((e['Package'],e['Test']),[]).append(e)
counts=collections.Counter(e[-1]['Action'] for e in roots.values())
relevant={n:[e for e in events if e.get('Test')==n or e.get('Test','').startswith(n+'/')] for n in ['TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire','TestChatLifecycleTerminalEvidenceRetention','TestChatLifecycleTerminalEvidenceCollectorRequiresTerminalAnchor','TestLocalThresholdPprofInterruptDuringRequestRegistration','TestLocalThresholdPprofInterruptJoinsRequestsAndWritesPartialMetadata']}
result=read(M/'result.json');command=read(M/'command.json');identity=read(M/'identity.json');policy=read(M/'policy.json');exp=read(O/'source-expectations.json')
assert identity['source_sha']==exp['checkout_sha'] and identity['policy_sha']==exp['base_sha']
assert hashlib.sha256((M/'policy.json').read_bytes()).hexdigest()==identity['policy_sha256']
check=policy['trusted_checks']['scripts-integration'];assert all(command[k]==check[k] for k in ['arguments','working_dir','timeout_seconds','max_output_bytes'])
assert command['timeout_seconds']==600 and command['max_output_bytes']==8388608 and '-timeout=9m' in command['arguments'] and '-parallel=2' in command['arguments']
assert len(raw)==result['output_bytes']
s={'artifact_id':a['id'],'identity':identity,'command':command,'result':result,'root_last_action_counts':dict(counts),'root_count':len(roots),'test_fail_events':[e for e in events if e.get('Test') and e['Action']=='fail'],'skips':[e for e in events if e['Action']=='skip'],'package_events':[e for e in events if not e.get('Test')],'relevant_tests':relevant,'raw_log_sha256':hashlib.sha256(raw).hexdigest(),'raw_log_bytes':len(raw),'coverage_limit':'Go parent PASS plus frozen 20-method/79-process-case fixture establishes execution of the complete named collector test; inner per-case Linux receipt is not directly in the uploaded artifact.'}
(O/'scripts-event-timeline.json').write_text(json.dumps(events,indent=2)+'\n');(O/'scripts-early-summary.json').write_text(json.dumps(s,indent=2)+'\n')
print(json.dumps({'status':result['status'],'elapsed_seconds':result['elapsed_seconds'],'exit_code':result['exit_code'],'wrapper_exit_code':result['wrapper_exit_code'],'roots':dict(counts),'relevant_terminal':{n:[{k:e[k] for k in ['Test','Action','Elapsed','Time'] if k in e} for e in es if e['Action'] in ['pass','fail','skip']] for n,es in relevant.items()},'cleanup':result['cleanup']},indent=2))
