import pathlib,subprocess,hashlib,json,runpy,datetime
repo=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
out=pathlib.Path('/tmp/wukongim-v3-readiness-20261021/cloud/post-systemd-repair-execution-options'); out.mkdir(exist_ok=False)
sha='ff0aa096fe1bd125be70fc1844b2f45cc7f0115e'
paths={
 'AGENTS.md':[(1,59),(172,216)],
 'internal/usecase/chatlifecyclerun/FLOW.md':[(1,100)],
 '.github/workflows/README.md':[(1016,1085),(1170,1218)],
 '.github/workflows/chat-lifecycle-rehearsal.yml':[(1,62)],
 '.github/workflows/chat-lifecycle-formal.yml':[(58,64),(97,135)],
 'configs/cloud/chat-lifecycle/rehearsal-v1.json':[(1,55)],
 'configs/cloud/chat-lifecycle/formal-v1.json':[(1,55)],
 'cmd/wkchatlifecycle/main.go':[(192,221)],
 'internal/usecase/chatlifecyclerun/run.go':[(113,136),(225,245),(405,488)],
 'scripts/chat-lifecycle/stage-orchestrate.sh':[(51,70),(138,155),(347,351),(559,573)],
 'scripts/chat-lifecycle/authenticate-released-preclock.py':[(127,142),(155,199),(254,259),(341,346),(517,554)],
 'scripts/chat-lifecycle/select-formal-transition-committed.jq':[(1,20)]}
freeze=[]; excerpts=[]
for p,ranges in paths.items():
 b=subprocess.check_output(['git','show',sha+':'+p],cwd=repo); freeze.append({'path':p,'sha256':hashlib.sha256(b).hexdigest(),'bytes':len(b)})
 lines=b.decode().splitlines(); excerpts += ['\n## '+p+'\n']
 for start,end in ranges:
  excerpts.append('\n'.join(f'{i}: {lines[i-1]}' for i in range(start,min(end,len(lines))+1)))
 if p.endswith('authenticate-released-preclock.py'):
  (out/'authority-helper-frozen.py').write_bytes(b)
(out/'source-freeze.json').write_text(json.dumps({'source_commit':sha,'source_role':'draft PR #1021 candidate; not a merged or paid-authorized control','product_sha':'fc18221282f289088ec9b4282fda8e0bfc67a3b8','files':freeze,'read_methods':['local git show','local retained public evidence'],'network_calls':0,'repository_edits':0},indent=2)+'\n')
(out/'source-excerpts.txt').write_text('\n'.join(excerpts)+'\n')
base=pathlib.Path('/tmp/wukongim-v3-readiness-20261021/cloud/rc-continuation-e302/terminal/verified')
module=runpy.run_path(str(out/'authority-helper-frozen.py'))
def denied(*args,**kwargs): raise RuntimeError('network not permitted in this offline check')
module['validate_cost'].__globals__['api']=denied
costs=[]
for label,root,attempt in [('original',base/'continuation/evidence/original','attempt-1'),('continuation',base,'attempt-2')]:
 names={'plan':root/attempt/'lease-plan.json','receipt':root/attempt/'receipt.json','quote':root/attempt/'quote.json','zero':root/'zero-inventory.json'}
 raw={k:p.read_bytes() for k,p in names.items()}; obj={k:json.loads(v) for k,v in raw.items()}
 zero=obj['zero']['result']['zero_inventory']; quote=obj['quote']['quote']; receipt=obj['receipt']['receipt']
 cost=module['validate_cost'](obj['plan'],receipt,quote,module['instant'](zero['observed_at']))
 costs.append({'label':label,'lease_id':receipt['lease_id'],'cost_method':'full_admitted_quote_reserve','amount_micros':cost,'quoted_at':quote['quoted_at'],'zero_observed_at':zero['observed_at'],'quote_to_zero_seconds':(module['instant'](zero['observed_at'])-module['instant'](quote['quoted_at'])).total_seconds(),'existing_pure_cost_coverage_check':'pass','input_files':{k:{'path':str(names[k]),'sha256':hashlib.sha256(v).hexdigest(),'bytes':len(v)} for k,v in raw.items()}})
reserve=sum(c['amount_micros'] for c in costs); assert reserve==148955200
result={'schema':'wukongim.cloud_rc.next_execution_options/v1','analysis_only':True,'executable_now':False,'existing_entrypoint_supports_required_budget':False,'draft_control_sha':sha,'future_merged_control_sha':None,'product_sha':'fc18221282f289088ec9b4282fda8e0bfc67a3b8','old_request':'chat-20261021T095421Z-cf11d65c','old_request_stopped':True,'existing_released_continuation_exhausted':True,'new_request_id':None,'new_paid_authorization':False,'reserve_micros':reserve,'reserve_is_invoice':False,'actual_bills':'unknown','task_hard_limit_micros':1500000000,'task_operational_stop_micros':1350000000,'remaining_hard_limit_micros':1500000000-reserve,'remaining_operational_stop_micros':1350000000-reserve,'new_request_default_committed_micros':0,'cost_coverage':costs,'probe_limits':['Pure existing validate_cost function on retained exact public bytes; no live API/authentication/billing call.','No current-control merge or CI status verification was performed.','No new Quote, capacity, OIDC identity, or cloud availability claim.'],'preferred_future_option':'Reviewed downward-only new-request budget binding carried through rehearsal and formal, while preserving old default and continuation authority; requires a code change and later exact paid authorization.','alternative_future_option':'Separate authenticated cross-request prior-cost proof with full original limits and committed=148955200; more identity/evidence/consumer work; not the existing released-preclock proof.'}
(out/'options.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'path':str(out),'reserve_micros':reserve,'remaining_hard':result['remaining_hard_limit_micros'],'remaining_stop':result['remaining_operational_stop_micros'],'offline_cost_checks':[c['existing_pure_cost_coverage_check'] for c in costs]}))
