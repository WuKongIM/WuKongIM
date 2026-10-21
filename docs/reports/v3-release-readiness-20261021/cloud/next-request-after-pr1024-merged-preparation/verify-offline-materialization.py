#!/usr/bin/env python3
"""Local structural checks only. No cloud, GitHub, key generation, or dispatch."""
import argparse, base64, datetime, hashlib, json, os, pathlib, struct, subprocess
p=argparse.ArgumentParser()
p.add_argument('--repo',required=True);p.add_argument('--source',required=True)
p.add_argument('--tool',required=True);p.add_argument('--output',required=True)
a=p.parse_args();out=pathlib.Path(a.output);out.mkdir(parents=True,exist_ok=False)
def write(name,obj):
    (out/name).write_text(json.dumps(obj,indent=2,ensure_ascii=False)+'\n')
def git(path):
    return subprocess.run(['git','-C',a.repo,'show',a.source+':'+path],check=True,capture_output=True).stdout
for stage in ['rehearsal','formal']:
    (out/(stage+'-template.json')).write_bytes(git('configs/cloud/chat-lifecycle/'+stage+'-v1.json'))
# Fixed public-format test vectors only: no private key and no operational identity.
def pub(byte):
    pack=lambda b:struct.pack('>I',len(b))+b
    return 'ssh-ed25519 '+base64.b64encode(pack(b'ssh-ed25519')+pack(bytes([byte])*32)).decode()
request='TEST-ONLY-offline-not-a-request';bundle='sha256:'+'a'*64
now='2026-10-21T00:00:00Z';limit=1276567200;stop=1126567200
common=['--source-sha',a.source,'--operator','tangtaoit','--codex-diagnostic-pubkey',pub(17),'--request-id',request,'--repository','WuKongIM/WuKongIM','--bundle-digest',bundle,'--deployment-pubkey',pub(34),'--now',now]
transition={'schema':'wukongim.chat_lifecycle.formal_transition/v1','from_stage':'rehearsal','outcome':'rehearsal_pass','request_id':request,'source_sha':a.source,'bundle_digest':bundle,'codex_diagnostic_pubkey':pub(17),'committed_micros':1234567,'zero_inventory':True}
write('TEST-ONLY-synthetic-transition.json',transition)
results=[]
def run(name,stage,cap=str(limit),prior=0,attempt=1,transition_path=None,reject=False):
    cmd=[a.tool,'materialize','--template',str(out/(stage+'-template.json')),*common,'--attempt',str(attempt),'--committed-micros',str(prior)]
    if cap is not None:cmd+=['--request-budget-limit-micros',cap]
    if transition_path:cmd+=['--transition',str(out/transition_path)]
    r=subprocess.run(cmd,capture_output=True,text=True,timeout=10,env={**os.environ,'GOPROXY':'off','GOSUMDB':'off'})
    (out/(name+'.stdout')).write_text(r.stdout);(out/(name+'.stderr')).write_text(r.stderr)
    item={'case':name,'argv':cmd,'exit_code':r.returncode,'expected':'invalid input' if reject else 'materialized','terminal':True}
    if reject:
        item['pass']=r.returncode==1 and r.stdout=='' and r.stderr.strip()=='chat lifecycle run: invalid input'
    else:
        plan=json.loads(r.stdout);lp=plan['lease_plan'];budget=lp['budget'];expected=1500000000 if cap is None else int(cap)
        duration=7200 if stage=='rehearsal' else 259200;lease=43200 if stage=='rehearsal' else 345600
        start=datetime.datetime.fromisoformat(now.replace('Z','+00:00'));end=datetime.datetime.fromisoformat(lp['expires_at'].replace('Z','+00:00'))
        checks={'exit_zero':r.returncode==0,'hard':budget['limit_micros']==expected,'stop':budget['operational_stop_micros']==expected-150000000==plan['operational_stop_micros'],'prior':budget['committed_micros']==prior,'duration':plan['workload_duration_seconds']==duration,'readiness':plan['readiness_timeout_seconds']==7200,'lease_duration':(end-start).total_seconds()==lease,'lease_identity':lp['lease_id']==request+'-'+stage+'-1','request':lp['request_id']==request,'source':lp['provenance']['source_sha']==a.source,'bundle':lp['provenance']['bundle_digest']==bundle,'four_hosts':sum(x['count'] for x in lp['host_groups'])==4,'compute':all(x['compute']['vcpus']==4 and x['compute']['memory_bytes']==8589934592 for x in lp['host_groups']),'ports':sorted(x['port_from'] for x in lp['network']['initial_access'])==[22,80],'access_expiry':all(x['until']==lp['expires_at'] for x in lp['network']['initial_access'])}
        item['checks']=checks;item['pass']=all(checks.values())
        write(name+'.TEST-ONLY-plan.json',plan)
    results.append(item)
run('lower-rehearsal','rehearsal')
run('default-rehearsal','rehearsal',cap=None)
run('lower-formal-synthetic-transition','formal',prior=1234567,transition_path='TEST-ONLY-synthetic-transition.json')
for name,kw in [('raise-cap',{'cap':'1500000001'}),('zero-stop',{'cap':'150000000'}),('padded-cap',{'cap':'01276567200'}),('historical-prior-in-new-request',{'prior':223432800}),('unproved-attempt-two',{'attempt':2})]:run(name,'rehearsal',reject=True,**kw)
run('formal-missing-transition','formal',reject=True)
write('results.json',{'schema':'local-only-offline-materialization/v1','executable_procurement':False,'source':a.source,'tool_sha256':hashlib.sha256(pathlib.Path(a.tool).read_bytes()).hexdigest(),'request_is_test_fixture':True,'synthetic_transition_is_authority':False,'provider_or_github_contact':False,'cases':results,'all_pass':all(x['pass'] for x in results),'limits':['Formal positive proves structural propagation only, not transition authenticity.','No Quote was fabricated or requested; live price, capacity, account, inventory and cleanup selector admission are unverified.','Old expired fixture time prevents treating these files as live plans.']})
print(json.dumps({'cases':len(results),'all_pass':all(x['pass'] for x in results)}))
raise SystemExit(0 if all(x['pass'] for x in results) else 1)
