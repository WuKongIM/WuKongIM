from pathlib import Path
import json,hashlib,re
p=Path(__file__).resolve().parent
read=lambda f:json.loads((p/f).read_text())
exp=read('source-expectations.json');head=read('head-commit.json');checkout=read('actual-checkout-commit.json');snap=p/(p/'latest-snapshot.txt').read_text().strip();run=json.loads((snap/'run.json').read_text());jobs=json.loads((snap/'jobs.json').read_text())['jobs']
assert run['head_sha']==exp['head_sha'] and run['run_attempt']==1 and run['status']=='completed' and run['conclusion']=='success'
assert checkout['sha']==exp['checkout_sha'] and checkout['tree']['sha']==head['tree']['sha']==exp['head_tree']
assert [x['sha'] for x in checkout['parents']]==[exp['base_sha'],exp['head_sha']]
result=[]
for j in jobs:
 row={k:j[k] for k in ['id','name','status','conclusion','started_at','completed_at','steps']}
 if j['conclusion']!='skipped':
  lp=p/'job-logs-public'/str(j['id'])/'complete.log';s=lp.read_text();row['log_sha256']=hashlib.sha256(lp.read_bytes()).hexdigest();row['checkout_verified_in_log']=exp['checkout_sha'] in s
  if j['id']!=119449875820:assert row['checkout_verified_in_log']
  else:row['checkout_limit']='Aggregate job has no checkout; consumes same-run dependency results.'
 result.append(row)
unit=(p/'job-logs-public/119447136975/complete.log').read_text()
commands=[]
for line in unit.splitlines():
 if '\x1b[36;1mGOWORK=off go test' in line:commands.append(re.sub(r'\x1b\[[0-9;]*m','',line).split(' ',1)[1])
baseline=(p/'job-logs-public/119447136848/complete.log').read_text();assert '{"reason": "performance_inputs_changed", "reuse": false}' in baseline
value={'identity':exp,'run_status':run['status'],'run_conclusion':run['conclusion'],'checkout_verified':True,'same_tree':True,'jobs':result,'baseline':{'reuse':False,'reason':'performance_inputs_changed'},'unit_race_commands':commands,'unit_race_successful_package_lines':len(re.findall(r'Z ok\s+',unit)),'limits':['This is exact-head PR evidence, not long-duration qualification.','Two conditional jobs are skipped; they are not passing validations.','Unit/race nonverbose output does not expose every internal skip.','No provider, SSH, dispatch, rerun, cancellation or publication operation performed.']}
(p/'final-source-and-jobs.json').write_text(json.dumps(value,indent=2)+'\n')
print(json.dumps({'success':sum(j['conclusion']=='success' for j in jobs),'skipped':sum(j['conclusion']=='skipped' for j in jobs),'unit_race_package_lines':value['unit_race_successful_package_lines']},indent=2))
