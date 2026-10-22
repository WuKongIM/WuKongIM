from pathlib import Path
import hashlib,json,re,datetime
r=Path(__file__).resolve().parent
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
b=json.loads((r/'baseline.json').read_text());head=b['head'];tree=b['head_and_reviewed_tree']
snap=sorted((r/'snapshots').iterdir())[-1];s=json.loads((snap/'summary.json').read_text())
assert s['head']==head and len(s['runs'])==2
assert all(x['head_sha']==head and x['event']=='push' and x['attempt']==1 and x['status']=='completed' and x['conclusion']=='success' for x in s['runs'])
assert len(s['checks'])==3 and sum(x['conclusion']=='success' for x in s['checks'])==2 and sum(x['conclusion']=='skipped' for x in s['checks'])==1
for run in s['runs']:
 d=json.loads((snap/(str(run['id'])+'-run.json')).read_text());assert d['head_branch']=='main'
merge=json.loads((r/'source-merge.json').read_text());reviewed=json.loads((r/'source-reviewed.json').read_text())
assert merge['sha']==head and merge['tree']['sha']==reviewed['tree']['sha']==tree
pages=json.loads((snap/'39836199206-jobs.json').read_text());sdk_jobs=json.loads((snap/'39836199218-jobs.json').read_text())
page_job=next(j for j in pages['jobs'] if j['id']==120025083053)
assert page_job['conclusion']=='success'
for name in ['Configure Pages','Upload complete Pages artifact']:
 step=next(st for st in page_job['steps'] if st['name']==name);assert step['conclusion']=='skipped'
assert next(j for j in pages['jobs'] if j['name']=='Deploy complete Pages snapshot')['conclusion']=='skipped'
assert len(json.loads((snap/'39836199206-artifacts.json').read_text())['artifacts'])==0
logs={}
for p in (r/'logs').glob('*.log'):
 t=p.read_text();assert head in t
 logs[p.name]={'bytes':p.stat().st_size,'sha256':sha(p),'actual_checkout':head}
assert len(logs)==2
page_log=(r/'logs/job-120025083053.log').read_text();sdk_log=(r/'logs/job-120025083121.log').read_text()
assert 'Ran 595 tests in 129.762s' in page_log and 'Ran 4 tests in 0.002s' in page_log and 'package repository contracts passed' in page_log and 'OK (skipped=' not in page_log
assert 'Ran 49 tests in 1.298s' in sdk_log and 'Ran 18 tests in 3.967s' in sdk_log and 'OK (skipped=' not in sdk_log
receipts=list((r/'artifacts').glob('*/provider-contract-receipt.json'));assert len(receipts)==1
receipt_path=receipts[0];receipt=json.loads(receipt_path.read_text())
assert receipt['checkout_sha']==head and receipt['checkout_tree']==tree and receipt['tests']==49 and len(receipt['test_names'])==49 and receipt['failures']==receipt['errors']==receipt['skips']==0 and receipt['provider_network_allowed'] is False
artifacts=[]
for p in (r/'artifacts').glob('*-validation.json'):
 d=json.loads(p.read_text());assert d['api_sha256_matched'] and d['zip_crc_verified']
 a=d['artifact_api'];z=r/'artifacts'/(str(a['id'])+'-'+a['name']+'.zip')
 assert d['downloaded_bytes']==z.stat().st_size==a['size_in_bytes'] and sha(z)==d['local_sha256']
 for m in d['members']:
  f=z.with_suffix('')/m['name'];assert f.stat().st_size==m['bytes'] and sha(f)==m['sha256']
 artifacts.append({'id':a['id'],'name':a['name'],'bytes':z.stat().st_size,'sha256':sha(z),'members':len(d['members'])})
assert len(artifacts)==1
old=[]
for name,facts in b['prior_groups'].items():
 p=r.with_name(name);assert sha(p/'SHA256SUMS')==facts['manifest_sha256'] and sha(p/'archive-summary.json')==facts['archive_summary_sha256']
 verified=0
 for line in (p/'SHA256SUMS').read_text().splitlines():
  digest,member=line.split(None,1);assert sha(p/member)==digest;verified+=1
 old.append({'group':name,'unchanged':True,'manifest_entries_verified':verified,'manifest_sha256':facts['manifest_sha256']})
summary={'schema':'wukongim/pr80-main-first-ci/v1','observed_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'head':head,'reviewed_head':b['reviewed_head'],'head_and_reviewed_tree':tree,'actual_checkout':head,'all_attempt_1':True,'all_runs_terminal':True,'final_observation':s,'pages':{'default_tests':595,'default_skips':0,'default_seconds':129.762,'contract_tests':4,'configure_pages':'skipped','upload_pages_artifact':'skipped','deploy':'skipped','artifact_count':0},'provider_sdk':{'tests':49,'seconds':1.298,'skips':0,'failures':0,'errors':0,'input_tests':18,'input_seconds':3.967,'input_skips':0,'provider_network_allowed':False,'receipt':receipt_path.relative_to(r).as_posix(),'receipt_sha256':sha(receipt_path)},'full_job_logs':logs,'public_artifact_zips':artifacts,'prior_groups':old,'read_only_monitor':True,'source_mutations':0,'dispatches':0,'reruns':0,'merges':0,'cloud_nas_or_key_operations':False,'production_stable_publication':False,'limits':'Only the automatic main push Pages and SDK workflows ran. The native drill has no main-push trigger; its prior first-attempt evidence remains separate and the identical source tree is proved. These offline TEST ONLY results do not establish public stable publication, live CDN/OSS acceptance or a v3 GA release.'}
(r/'validation-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
(r/'README.md').write_text(f'''# packages PR #80 first main push CI\n\nPR80 merged at {b['merged_at']} with exact merge tip {head}. GitHub commit APIs prove its tree {tree} equals reviewed PR head {b['reviewed_head']}; both complete job logs and the SDK typed receipt bind the actual checkout to the merge tip.\n\nThe two automatic main-push workflows completed successfully on attempt 1. Pages passed 595 tests in 129.762 s, zero skips, plus four repository contracts. Configure Pages, artifact upload and deployment were deliberately skipped; no Pages artifact was produced. Official SDK contracts passed 49 tests in 1.298 s and input regressions passed 18 tests in 3.967 s, zero skips. The SDK typed receipt records zero errors/failures/skips and provider_network_allowed=false.\n\nThe one 1682-byte public SDK artifact ZIP was fully downloaded, matched against GitHub API size/SHA-256, CRC-verified and its sole member hashed. Full job logs and complete bounded API enumerations are retained. Earlier generation 1 and generation 2 manifests and every retained member revalidated unchanged.\n\nNo dispatch, retry, source mutation, merge, permission, cloud, NAS or key operation was performed by this collector. Native tests did not run again on main push because that workflow has no push trigger; prior native evidence remains separate, with exact source-tree equality. This is not production stable publication or v3 GA acceptance.\n''')
files=sorted(p for p in r.rglob('*') if p.is_file() and p.name not in {'PUBLIC_ALLOWLIST.json','SHA256SUMS','archive-summary.json'})
assert all(not p.is_symlink() for p in files)
records=[{'path':p.relative_to(r).as_posix(),'size':p.stat().st_size,'sha256':sha(p)} for p in files]
(r/'PUBLIC_ALLOWLIST.json').write_text(json.dumps({'schema':'wukongim/public-evidence-allowlist/v1','files':records,'contains_private_material':False},indent=2)+'\n')
files=sorted(files+[r/'PUBLIC_ALLOWLIST.json']);manifest=''.join(sha(p)+'  '+p.relative_to(r).as_posix()+'\n' for p in files).encode();(r/'SHA256SUMS').write_bytes(manifest)
a={'payload_files':len(records),'payload_bytes':sum(x['size'] for x in records),'manifest_entries':len(files),'manifest_sha256':hashlib.sha256(manifest).hexdigest(),'result':'passed_first_main_pages_and_sdk'}
(r/'archive-summary.json').write_text(json.dumps(a,indent=2)+'\n');print(json.dumps(a))
