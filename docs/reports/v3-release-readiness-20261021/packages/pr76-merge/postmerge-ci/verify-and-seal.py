#!/usr/bin/env python3
"""Seal only explicitly named natural postmerge GET evidence; no candidate execution."""
import datetime,hashlib,json,pathlib,re,tarfile,zipfile,zlib
P=pathlib.Path(__file__).resolve().parent
HEAD='be32eb375e3c77dc5b3d1070fd798c281ae2d3bc';TREE='e319ee72e8d30f012fd5e6206d131e2b75c47a09'
def sha(x):return hashlib.sha256(x).hexdigest()
def read(n):return json.loads((P/n).read_bytes())
def write(n,x):(P/n).write_text(json.dumps(x,indent=2)+'\n')
now=datetime.datetime.now(datetime.timezone.utc).isoformat()
commit=read('commit.json');assert commit['sha']==HEAD and commit['tree']['sha']==TREE
runs=read('runs-final.json')['workflow_runs'];assert {r['id'] for r in runs}=={39651431863,39651432004}
assert all(r['head_sha']==HEAD and r['head_branch']=='main' and r['event']=='push' and r['run_attempt']==1 and r['status']=='completed' and r['conclusion']=='success' for r in runs)
jobs=read('pages-jobs-3.json')['jobs'];sdkjobs=read('sdk-jobs-final.json')['jobs']
assert [(j['id'],j['conclusion']) for j in jobs]==[(119400470757,'success'),(119401548760,'skipped')]
assert len(sdkjobs)==1 and sdkjobs[0]['id']==119400471277 and sdkjobs[0]['conclusion']=='success'
checks=read('checks-final.json')['check_runs'];assert len(checks)==3 and all(x['status']=='completed' and x['conclusion']==('skipped' if x['id']==119401548760 else 'success') for x in checks)
page=(P/'pages-job.log').read_text();sdk=(P/'sdk-job.log').read_text()
assert HEAD in page and HEAD in sdk
assert re.findall(r'Ran (\d+) tests in ',page)==['4','567'] and re.findall(r'Ran (\d+) tests in ',sdk)==['22','10']
assert not re.search(r'OK \(skipped=|FAILED \(',page) and not re.search(r'OK \(skipped=|FAILED \(',sdk)
assert len(re.findall(r'Z OK\r?$',page,re.M))==2 and len(re.findall(r'Z OK\r?$',sdk,re.M))==2
receipt=read('provider-contract-receipt.json')
assert receipt['checkout_sha']==HEAD and receipt['checkout_tree']==TREE and receipt['tests']==22 and len(receipt['test_names'])==22 and receipt['skips']==receipt['failures']==receipt['errors']==0
assert receipt['provider_network_allowed'] is False and receipt['lock_sha256']=='0f5cc44660d936c1f3d57093ae07532fef949028b2608145758d1bdb5e5bfb9b'
assert read('channels.json')['channels']['preview']['enabled'] is True
assert read('pages-artifacts-final.json')['total_count']==0
art=read('sdk-artifacts-final.json')['artifacts'];assert len(art)==1 and art[0]['id']==12370266347
z=P/'object-storage-sdk-contract.zip';assert 'sha256:'+sha(z.read_bytes())==art[0]['digest']
with zipfile.ZipFile(z) as archive:
 assert archive.testzip() is None and archive.namelist()==['provider-contract-receipt.json']
 info=archive.infolist()[0];raw=archive.read(info);assert raw==(P/info.filename).read_bytes() and (zlib.crc32(raw)&0xffffffff)==info.CRC
source={'recorded_at':now,'merged_commit':HEAD,'tree':TREE,'same_tree_as_reviewed_PR_head':'dded26afa12188d5f1c036af4b9c06c3cbddb46d','GitHub_commit_API_tree_verified':True,'both_checkout_logs_contain_exact_head':True,'SDK_receipt_exact_head_tree':True,'SDK_lock_sha256':receipt['lock_sha256'],'workflow_source_blob':read('pages-source.json')['sha'],'channels_source_blob':read('channels-source.json')['sha'],'read_scope':'Only natural postmerge push runs on this exact commit. No unrelated main run collected.'}
write('source-binding.json',source)
summary={'recorded_at':now,'head':HEAD,'tree':TREE,'all_runs_terminal':True,'all_validations_passed':True,'natural_push_only':True,'attempt':1,'runs':[{k:r[k] for k in ['id','name','html_url','path','event','head_sha','run_attempt','status','conclusion','created_at','updated_at']} for r in runs],'Pages':{'job':119400470757,'tests':567,'skips':0,'contracts':4,'conclusion':'success','deployment_job':119401548760,'deployment_conclusion':'skipped','deployment_reason':'preview is enabled; immutable Pages workflow sets deploy_bootstrap=false','Pages_artifacts':0,'site_published_by_this_run':False},'SDK':{'job':119400471277,'tests':22,'inputs':10,'skips':0,'conclusion':'success','hash_locked_install':'success','receipt':{'sha256':sha((P/'provider-contract-receipt.json').read_bytes()),'bytes':(P/'provider-contract-receipt.json').stat().st_size},'artifact_id':12370266347,'artifact_SHA_and_CRC_verified':True},'native_workflow':'No natural postmerge native run found for this exact commit; premerge native attempt2 remains separate evidence, not a new postmerge execution.','test_log_note':'Two printed passed=false/cases=0 preflight JSON fixture outputs follow unittest OK; the full Pages job is success. They are not native drill runs or hidden test failures.','retained_limits':['Original Standards tests-after-code P2 remains disclosed.','Original native attempt1 failure remains preserved and not uniquely attributed.','No production activation, live provider success, public release, or complete new D2 production-shaped native closure claimed.'],'actions':'GET-only plus bounded local public evidence writes; no dispatch/retry/cancel/source edit/provider/credential access. All collector commands terminal.'}
write('summary.json',summary)
(P/'README.md').write_text('''# PR #76 natural postmerge CI — complete

Merged commit `be32eb375e3c77dc5b3d1070fd798c281ae2d3bc` has the reviewed tree `e319ee72e8d30f012fd5e6206d131e2b75c47a09`. Both natural `push` runs are attempt 1 and completed successfully; both checkout logs and the SDK receipt bind this exact commit.

- [Pages 39651431863](https://github.com/WuKongIM/packages/actions/runs/39651431863): **567 tests, zero skips; four existing contracts passed**. Validation job `119400470757` succeeded. Deployment job `119401548760` was skipped: the immutable channel control has preview enabled, so the bootstrap-only workflow sets `deploy_bootstrap=false`. No Pages artifact or deployment was produced.
- [SDK 39651432004](https://github.com/WuKongIM/packages/actions/runs/39651432004): hash-locked installation succeeded; **22 SDK and 10 input tests, zero skips**. Job `119400471277` succeeded. New receipt records zero failures/errors/skips, exact commit/tree and lock SHA `0f5cc44660d936c1f3d57093ae07532fef949028b2608145758d1bdb5e5bfb9b`. Provider networking remained disallowed in tests.

Artifact `12370266347` is 1032 bytes, SHA-256 `9b313ab725a2d198dbe2a1d32fc92163de79e53665bc8171cfa145afa7b85620`; its archive digest, complete member CRC and extracted receipt bytes were verified. Both full executed-job logs, terminal metadata, immutable deployment-condition sources and this small receipt are retained. Skipped deployment had no executed steps/log.

Only these exact-commit runs were collected. There was no natural postmerge native run; the premerge 49-case success belongs to its separately preserved second attempt. The original first failure is retained and its cause remains unknown. The historical tests-after-code process P2 remains disclosed. These results do not establish production activation, a public release, live provider success or complete new D2 production-shaped native integration.

All collector processes terminated. No dispatch, retry, cancellation, source edit, credential/provider operation, push or merge occurred here. Root owns merge ancestry and local worktree cleanup. `summary.json`, `source-binding.json`, `sdk-artifact-binding.json`, the finite `public-allowlist.json` and final manifest bind this evidence.
''')
names=['README.md','initial-binding.json','commit.json','runs-initial.json','checks-initial.json','pages-jobs-1.json','pages-jobs-2.json','pages-run-3.json','pages-jobs-3.json','pages-artifacts-final.json','sdk-jobs-final.json','sdk-artifacts-final.json','sdk-run-final.json','sdk-job.log','pages-job.log','pages-source.json','pages-source.yml','channels-source.json','channels.json','object-storage-sdk-contract.zip','provider-contract-receipt.json','sdk-artifact-binding.json','runs-final.json','checks-final.json','source-binding.json','summary.json','verify-and-seal.py','collector-first-failure.json']
assert len(names)==len(set(names))==28
actual={x.name for x in P.iterdir() if x.is_file()};assert actual==set(names)|{'public-allowlist.json'},(actual-set(names),set(names)-actual)
write('public-allowlist.json',{'schema':'bounded-public-evidence-allowlist/v1','payload_paths':names,'sealing_paths':['public-allowlist.json','final-manifest.json','public-evidence.tar.gz','public-archive-binding.json'],'scope':'Only be32 postmerge attempt1 exact two runs. No private material or unlisted files.'})
files=[]
for name in names+['public-allowlist.json']:
 raw=(P/name).read_bytes();assert len(raw)<1024*1024
 for kind in ('PRIVATE KEY','RSA PRIVATE KEY','EC PRIVATE KEY','PGP PRIVATE KEY BLOCK'):
  pattern=('-----BEGIN '+kind+'-----').encode();assert pattern not in raw
 files.append({'path':name,'bytes':len(raw),'sha256':sha(raw)})
write('final-manifest.json',{'sealed_at':now,'head':HEAD,'terminal':True,'files':files,'total_bytes':sum(x['bytes'] for x in files),'all_allowlisted':True,'no_private_key_blocks_detected':True})
with tarfile.open(P/'public-evidence.tar.gz','w:gz') as archive:
 for f in files:archive.add(P/f['path'],arcname='pr76-postmerge-ci/'+f['path'],recursive=False)
 archive.add(P/'final-manifest.json',arcname='pr76-postmerge-ci/final-manifest.json',recursive=False)
with tarfile.open(P/'public-evidence.tar.gz') as archive:
 for f in files:
  raw=archive.extractfile('pr76-postmerge-ci/'+f['path']).read();assert len(raw)==f['bytes'] and sha(raw)==f['sha256']
binding={'archive':'public-evidence.tar.gz','bytes':(P/'public-evidence.tar.gz').stat().st_size,'sha256':sha((P/'public-evidence.tar.gz').read_bytes()),'manifest_sha256':sha((P/'final-manifest.json').read_bytes()),'payload_count':len(files),'archive_verified':True}
write('public-archive-binding.json',binding)
print(json.dumps({'success':True,'head':HEAD,'Pages_tests':567,'Pages_skips':0,'contracts':4,'SDK':22,'inputs':10,'deploy':'skipped','archive':binding}))
