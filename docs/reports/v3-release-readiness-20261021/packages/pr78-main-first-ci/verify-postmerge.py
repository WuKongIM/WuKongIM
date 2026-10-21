from pathlib import Path
import json,hashlib,re,zlib
P=Path(__file__).resolve().parent
R=lambda n:json.loads((P/n).read_bytes());sha=lambda b:hashlib.sha256(b).hexdigest()
def put(n,v):(P/n).write_text(json.dumps(v,sort_keys=True,indent=2)+'\n')
e=R('expected.json');h=R('main-commit.json');assert h['sha']==e['main_head'] and h['tree']['sha']==e['tree']
runs=R('discovery-final.json')['workflow_runs'];assert len(runs)==2
assert {x['id'] for x in runs}=={39674980407,39674980428}
for r in runs:assert r['head_sha']==e['main_head'] and r['event']=='push' and r['run_attempt']==1 and r['status']=='completed' and r['conclusion']=='success'
proof={}
for label,run,count in [('pages',39674980407,[4,581]),('sdk',39674980428,[34,10])]:
 for part in ('run','jobs','artifacts'):
  f=sorted(P.glob(f'snapshots/*/{run}-{part}.json'))[-1];(P/(label+'-'+part+'-final.json')).write_bytes(f.read_bytes())
 log=(P/(label+'-job.log')).read_text();ids=re.findall(r'git log -1 --format=%H\n[^\n]*? ([0-9a-f]{40})\n',log)
 assert ids==[e['main_head']] and list(map(int,re.findall(r'Ran (\d+) tests',log)))==count and not re.search('FAILED|skipped=',log)
 proof[label]={'run':run,'actual_checkout':ids[0],'tests_in_log_order':count,'test_skips':0,'log_sha256':sha(log.encode())}
checks=R('checks-final.json')['check_runs'];assert len(checks)==3 and all(c['head_sha']==e['main_head'] and c['status']=='completed' for c in checks)
assert sorted(c['conclusion'] for c in checks)==['skipped','success','success']
jobs=R('pages-jobs-final.json')['jobs'];build=next(j for j in jobs if j['name']=='Validate complete Pages snapshot');deploy=next(j for j in jobs if j['name']=='Deploy complete Pages snapshot');assert build['conclusion']=='success' and deploy['conclusion']=='skipped'
steps={s['name']:s['conclusion'] for s in build['steps']};assert steps['Configure Pages']==steps['Upload complete Pages artifact']=='skipped'
assert R('pages-artifacts-final.json')['total_count']==0
channels=R('channels.json.source');assert channels['channels']['preview']['enabled'] is True and channels['channels']['stable']['enabled'] is False
wf=(P/'pages.yml.source').read_text();assert ".channels.preview.enabled" in wf and "needs.build.outputs.deploy_bootstrap == 'true'" in wf
sd=P/'artifacts/object-storage-sdk-contract';rec=R('artifacts/object-storage-sdk-contract/download-receipt.json');artifact=rec['artifact'];assert artifact['workflow_run']['head_sha']==e['main_head'] and artifact['workflow_run']['id']==39674980428
assert rec['whole_zip_verified_against_api_digest'] and rec['all_members_crc_verified'] and rec['zip_size']==artifact['size_in_bytes']
for m in rec['members']:
 b=(sd/m['path']).read_bytes();assert len(b)==m['size'] and sha(b)==m['sha256'] and f'{zlib.crc32(b)&0xffffffff:08x}'==m['crc32']
sdk=R('artifacts/object-storage-sdk-contract/provider-contract-receipt.json');assert sdk['checkout_sha']==e['main_head'] and sdk['checkout_tree']==e['tree'] and sdk['tests']==34 and sdk['errors']==sdk['failures']==sdk['skips']==0 and not sdk['provider_network_allowed']
prior=P.parent/'pr78-ci-generation-1/archive-sha256.json';assert sha(prior.read_bytes())=='7a6842bd643a265c99e28501fd4771b9c42dd373863982d15160a14fa7ef7e13'
summary={'main_head':e['main_head'],'tree':e['tree'],'tree_equal_accepted_PR':True,'natural_push_attempt':1,'both_runs_terminal_success':True,'checks_success':2,'checks_skipped':1,'proof':proof,'deploy':{'job_id':deploy['id'],'conclusion':'skipped','Configure_Pages':'skipped','Upload_Pages':'skipped','Pages_artifact_count':0,'source_condition':'preview.enabled=true yields deploy_bootstrap=false; deploy requires true','stable_enabled':False},'SDK_artifact':{'whole_ZIP_SHA_recomputed':rec['zip_sha256_recomputed'],'API_digest_verified':True,'all_members_SHA_CRC_size_verified':True},'native_reexecution':False,'prior_accepted_native_run':39672398441,'prior_evidence_manifest_sha256':sha(prior.read_bytes()),'observer_rerun_dispatch_merge_source_cloud_actions':0,'all_observer_processes_terminal':True}
put('summary.json',summary);put('job-step-outcomes.json',{label:[{'id':j['id'],'name':j['name'],'conclusion':j['conclusion'],'steps':[{'number':s['number'],'name':s['name'],'conclusion':s['conclusion']} for s in j['steps']]} for j in R(label+'-jobs-final.json')['jobs']] for label in ('pages','sdk')})
print(json.dumps({'passed':True,'Pages':581,'contracts':4,'SDK':34,'input':10,'test_skips':0,'production_deploy':'skipped','same_accepted_tree':True,'native_reexecuted':False}))
