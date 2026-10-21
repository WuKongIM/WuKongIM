from pathlib import Path
import json,hashlib,collections,re,zipfile,datetime
out=Path(__file__).resolve().parent
private=out.parent/'candidate-private'
def read(p):return json.loads(p.read_text())
def save(name,value):
 (out/name).write_text(json.dumps(value,indent=2,ensure_ascii=False)+'\n')
def sha(b):return hashlib.sha256(b).hexdigest()
def events(b):
 values=[]
 for line in b.splitlines():
  try:v=json.loads(line)
  except (json.JSONDecodeError,UnicodeDecodeError):continue
  if isinstance(v,dict):values.append(v)
 return values
snap=out/(out/'latest-snapshot.txt').read_text().strip()
run=read(snap/'run.json');jobs=read(snap/'jobs.json')['jobs'];identity=read(out/'source-expectations.json')
head=read(out/'head-commit.json');checkout=read(out/'actual-checkout-commit.json')
assert run['head_sha']==identity['head_sha'] and run['run_attempt']==1 and run['status']=='completed'
assert head['sha']==identity['head_sha'] and checkout['sha']==identity['checkout_sha']
assert head['tree']['sha']==checkout['tree']['sha']==identity['head_tree']
assert [x['sha'] for x in checkout['parents']]==[identity['base_sha'],identity['head_sha']]
job_binding=[]
for j in jobs:
 log=out/'job-logs-public'/str(j['id'])/'complete.log'
 assert j['status']=='completed' and log.exists()
 text=log.read_text();assert identity['checkout_sha'] in text
 job_binding.append({'job_id':j['id'],'name':j['name'],'conclusion':j['conclusion'],'started_at':j['started_at'],'completed_at':j['completed_at'],'checkout_sha_present':True,'head_sha_present':identity['head_sha'] in text,'job_log_sha256':sha(log.read_bytes()),'steps':j['steps']})
save('final-source-and-jobs.json',{'identity':identity,'checkout_verified':True,'same_tree':True,'parent_shas':[x['sha'] for x in checkout['parents']],'jobs':job_binding,'run_conclusion':run['conclusion']})
def artifact_id(prefix):
 return next(a['id'] for a in read(snap/'artifacts.json')['artifacts'] if a['name'].startswith(prefix))
default_id=artifact_id('v3-candidate-default-');scripts_id=artifact_id('v3-candidate-scripts-');e2e_id=artifact_id('v3-candidate-e2e-')
def artifact(id):return out/'artifacts-public'/str(id)/'members'
default=artifact(default_id);scripts=artifact(scripts_id);e2e=artifact(e2e_id)
for p in [default,scripts]:
 i=read(p/'identity.json');assert i['source_sha']==identity['checkout_sha'] and i['policy_sha']==identity['base_sha'] and i['run_id']==str(run['id'])
 policy=read(p/'policy.json');cmd=read(p/'command.json');check=policy['trusted_checks'][cmd['named_check']]
 assert all(cmd[k]==check[k] for k in ['arguments','working_dir','timeout_seconds','max_output_bytes'])
 assert sha((p/'policy.json').read_bytes())==i['policy_sha256']
dtext=(default/'stdout-stderr.log').read_text()
dsummary={'artifact_id':default_id,'command':read(default/'command.json'),'result':read(default/'result.json'),'tested_package_count':len(re.findall(r'^ok\s+',dtext,re.M)),'no_test_files_package_count':len(re.findall(r'^\?\s+.*\[no test files\]$',dtext,re.M)),'limit':'Nonverbose go test summary does not enumerate all internal subtests/skips.'}
save('default-summary.json',dsummary)
with zipfile.ZipFile(private/(str(scripts_id)+'.zip')) as z:raw=z.read('stdout-stderr.log')
ev=events(raw);assert len(raw)==read(scripts/'result.json')['output_bytes']
safe=[{k:x[k] for k in ['Time','Action','Package','Test','Elapsed'] if k in x} for x in ev if x.get('Action') in ['start','run','pause','cont','pass','fail','skip']]
save('scripts-event-timeline.json',safe)
roots={};packages=[]
for x in safe:
 if x.get('Test') and '/' not in x['Test']:
  key=(x['Package'],x['Test']);roots.setdefault(key,[]).append(x)
 elif 'Test' not in x and x['Action'] in ['start','pass','fail','skip']:packages.append(x)
terminal=collections.Counter(x[-1]['Action'] for x in roots.values() if x[-1]['Action'] in ['pass','fail','skip'])
incomplete=[{'package':k[0],'test':k[1],'last_event':v[-1],'events':v} for k,v in roots.items() if v[-1]['Action'] not in ['pass','fail','skip']]
new='TestLocalThresholdPprofInterruptDuringRequestRegistration'
relevant={}
for name in [new,'TestLocalThresholdPprofInterruptJoinsRequestsAndWritesPartialMetadata','TestChatLifecycleTerminalEvidenceRetention','TestChatLifecycleTerminalEvidenceCollectorRequiresTerminalAnchor','TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire']:
 relevant[name]=[x for x in safe if x.get('Test')==name or x.get('Test','').startswith(name+'/')]
new_pass=[x for x in safe if x['Action']=='pass' and x.get('Test','').startswith(new+'/')];assert len(new_pass)==9
ssummary={'artifact_id':scripts_id,'command':read(scripts/'command.json'),'result':read(scripts/'result.json'),'root_test_terminal_counts':dict(terminal),'root_test_count':len(roots),'incomplete_root_count':len(incomplete),'incomplete_roots':incomplete,'test_fail_events':[x for x in safe if x['Action']=='fail' and 'Test' in x],'package_events':packages,'skip_events':[x for x in safe if x['Action']=='skip'],'relevant_tests':relevant,'new_registration_cases_passed':len(new_pass),'retention_limit':'Actual parent terminal events are retained below; the fixed 20-method/79-case inner receipt is created in Go TempDir and is not directly uploaded. Do not infer completion when the parent lacks PASS.','command_outcome':read(scripts/'result.json')['status'],'raw_log_bytes':len(raw),'raw_log_sha256':sha(raw)}
save('scripts-summary.json',ssummary)
allcleanup=[]
for label,p in [('default',default),('scripts',scripts)]+[(x,e2e/x) for x in ['build','ordinary_history','cross_node_delivery','self_chat']]:
 r=read(p/'result.json');c=r['cleanup'];assert c['verified'] and c['complete'] and c['remaining_processes']==[]
 allcleanup.append({'step':label,'command_status':r['status'],'elapsed_seconds':r['elapsed_seconds'],'cleanup':c})
save('owned-session-cleanup.json',allcleanup)
eventse=read(e2e.parent/'test-events.json');history=[]
for x in events((e2e/'ordinary_history/stdout.jsonl').read_bytes()):
 m=re.search(r'history verified: phase=(\S+) node=(\d+) rows=(\d+) temporary_retries=(\d+)',x.get('Output',''))
 if m:history.append({'test':x['Test'],'phase':m[1],'node':int(m[2]),'rows':int(m[3]),'temporary_retries':int(m[4])})
assert len(history)==55
selfreport=read(e2e/'self_chat/report.json');binary=(e2e/'binary.sha256').read_text().split()[0]
selfcases=[]
for c in selfreport['cases']:
 assert c['passed'] and c['binary_sha256']==binary
 selfcases.append({**{k:c[k] for k in ['nodes','phase','passed','binary_sha256','invalid_auth_refused','whole_cluster_restarts']},'acknowledged':len(c['acknowledged']),'history_checks':len(c['history']),'all_history_complete':all(h['complete'] for h in c['history'])})
e2e_job=next(j for j in jobs if j['name']=='Candidate migration and messaging E2E')
log=(out/'job-logs-public'/str(e2e_job['id'])/'complete.log').read_text()
checks=[line for line in log.splitlines() if 'wukongim-e2e: OK' in line]
assert len(checks)==4
esummary={'artifact_id':e2e_id,'identity':read(e2e/'identity.json'),'commands':{n:read(e2e/n/'command.json') for n in ['build','ordinary_history','cross_node_delivery','self_chat']},'results':{n:read(e2e/n/'result.json') for n in ['build','ordinary_history','cross_node_delivery','self_chat']},'terminal_events':[x for x in eventse if x['Action'] in ['pass','fail','skip']],'history_verifications':history,'history_verification_count':len(history),'binary_sha256':binary,'binary_sha256_check_count':len(checks),'binary_sha256_check_log_lines':checks,'self_chat':{**{k:selfreport[k] for k in ['passed','hash_slots','token_authentication','suppression_window_ms']},'cases':selfcases},'limits':'Three specified E2E packages run their default test sets. The opt-in 2,000-QPS replica comparison is skipped; this is not an E2E full-tree performance run.'}
save('e2e-summary.json',esummary)
print(json.dumps({'default':{k:dsummary[k] for k in ['tested_package_count','no_test_files_package_count']},'scripts':{'terminal':dict(terminal),'incomplete':len(incomplete),'new9':len(new_pass)},'history':len(history),'cleanups':len(allcleanup),'binary':binary},indent=2))
