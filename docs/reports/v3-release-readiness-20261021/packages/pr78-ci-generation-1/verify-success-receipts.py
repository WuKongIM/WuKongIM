"""Offline acceptance checks for exact packages PR78 generation 1 receipts."""
from pathlib import Path
import hashlib,json,re,gzip
P=Path(__file__).resolve().parent
R=lambda x:json.loads((P/x).read_text())
E=R('expected.json');H=E['head'];T=E['tree'];PRODUCT='fc18221282f289088ec9b4282fda8e0bfc67a3b8'
M=R('actual-checkout.json');assert R('head-commit.json')['sha']==H
assert R('head-commit.json')['tree']['sha']==M['tree']['sha']==T
assert [x['sha'] for x in M['parents']]==[E['base'],H]
for label in ['pages','native','sdk']:
 r=R(label+'-run-final.json');assert r['head_sha']==H and r['run_attempt']==1 and r['event']=='pull_request' and r['status']=='completed'
 assert r['conclusion']=='success'
legacy=[]
for name,n,nt in [('native-source-metadata',7,0),('native-local-signing',10,0),('native-bootstrap-preparation',6,0),('native-stable-candidate',31,8)]:
 v=R('artifacts/'+name+'-test-only/'+name+'.json');assert v['passed'] and len(v['cases'])==n and len(v.get('transport_cases',[]))==nt
 assert all(c['passed'] for c in v['cases']+v.get('transport_cases',[]))
 clean={k:v[k] for k in ['checked_in_control_unchanged','secret_tree_removed_before_verification','source_inputs_unchanged','scratch_removed'] if k in v};assert all(clean.values())
 legacy.append({'name':name,'cases':n,'transport_cases':nt,'cleanup':clean})
D=R('artifacts/native-stable-four-clients-test-only/native-stable-clients.json')
assert D['control']=={'checkout_sha':M['sha'],'tree':T}
assert D['test_only'] and D['passed'] and len(D['cases'])==49 and len({c['case'] for c in D['cases']})==49
assert all(c['passed'] for c in D['cases'])
assert all(D[x] is False for x in ['published','public_origin_verified','source_provenance_verified'])
for k in ['reader_cleanup','cleanup']:
 assert D[k]['verified'] and D[k]['errors']==[] and D[k]['remaining']=={'containers':[],'networks':[]}
assert len(D['cache_cleanup'])==4 and all(x['verified'] and x['result']=={'remaining':[]} for x in D['cache_cleanup'])
baseline=[];rpm_baseline={}
for gen,ver in [('A','3.0.0'),('B','3.0.1')]:
 v=R('artifacts/native-stable-four-clients-test-only/clients-'+gen+'.json')
 assert v['test_only'] and v['installed_cli_verified'] and v['expected_version_verified'] and v['expected_source_sha']==PRODUCT and v['expected_version']==ver
 clients=v['apt']+v['rpm'];assert len(clients)==4
 for c in clients:
  ins=c['installed_cli'];assert ins['verified'] and ins['identity']['commit']==PRODUCT and ins['identity']['version']==ver and ins['identity']['build_source']=='release'
  assert ins['checks']==['version','help','bench_validate','db_query','migrate_diagnose','invalid_inputs','read_only_data']
  assert c['download']['snapshot_versions']==[ver]
  baseline.append({'generation':gen,'distribution':c['distribution'],'identity':ins['identity'],'checks':ins['checks'],'download':c['download']})
  if c in v['rpm']:rpm_baseline[(gen,c['distribution'])]=c['download']
requests=[json.loads(x) for x in (P/'artifacts/native-stable-four-clients-test-only/https-requests.jsonl').read_text().splitlines()]
cache=[];switch=[]
for c in D['cases']:
 r=c.get('result',{});ca=r.get('cache')
 if ca:
  if ca['reuse_required']:assert ca['metadata_reads']
  for path in ca['metadata_reads']:
   before,after=ca['before'][path],ca['after'][path]
   assert all(before[k]==after[k] for k in ['device','inode','sha256','size']) and after['atime_ns']>1
  cache.append({'case':c['case'],'reuse_required':ca['reuse_required'],'metadata_reads':ca['metadata_reads'],'before_count':len(ca['before']),'after_count':len(ca['after'])})
 if c['expected_rejection'] and 'exit_code' in r:
  assert r['exit_code'] not in (0,124,125,126,127,137,143)
  matches=[x for x in D['commands'] if x['output_sha256']==r['output_sha256'] and x['exit_code']==r['exit_code']]
  assert matches and any(not x['timed_out'] and not x['output_limit'] for x in matches)
 fault=r.get('by_hash_fault_response')
 if fault:
  assert fault in requests and fault['response_write_completed'] and fault['status']==(404 if r['fault']=='missing-byhash' else 200)
  if r['fault']=='corrupt-byhash':assert fault['sha256']=='54958456b8d2c755a77144f155197b215e11eb47703fa0123c3659787ab7ca04' and fault['size']==28
 if c['case'] in ['rocky-linux-9-A-to-B-request-barrier','alma-linux-9-A-to-B-request-barrier']:
  dist=c['case'].removesuffix('-A-to-B-request-barrier');obs=r['requests'];serials={x['serial'] for x in obs};assert len(serials)==1
  assert all(x in requests and x['status']==200 and x['response_write_completed'] for x in obs)
  meta=[x for x in obs if '/repodata/' in x['path']];assert meta and all(x['path'].startswith('/snapshots/stable/r4242-') for x in meta)
  payload=[x for x in obs if '/Packages/' in x['path']];assert payload and all(x['path'].startswith('/snapshots/stable/r4243-') for x in payload)
  expected=rpm_baseline[('A',dist)];assert r['download']['snapshot_versions']==['3.0.0'] and r['download']['sha256']==expected['sha256'] and r['download']['size']==expected['size']
  assert all(x['sha256']==expected['sha256'] and x['size']==expected['size'] for x in payload)
  prefix=payload[0]['path'].split('site/rpm/',1)[0];body=('https://packages.githubim.com'+prefix+'site/rpm/stable/el/9/x86_64/\n').encode()
  mirrors=[(i,x) for i,x in enumerate(obs) if x['path']=='/rpm/stable/el/9/x86_64/mirrorlist' and x['sha256']==hashlib.sha256(body).hexdigest() and x['size']==len(body)]
  assert mirrors and any(obs.index(x)>mirrors[0][0] for x in payload)
  assert all(x['retained_alias'] and x['source']['audit_release_id']==4242 for x in r['rpm_requests']['payloads'])
  match=[(i,x) for i,x in enumerate(D['commands']) if 'download-'+dist+'-'+str(next(iter(serials))) in '\n'.join(x['command']) and 'metadata_expire=-1' in '\n'.join(x['command'])]
  assert len(match)==1
  command='\n'.join(match[0][1]['command']);assert command.count('--setopt=wukongim-stable.metadata_expire=-1')==2
  assert 'metadata_expire=-1 makecache --refresh' in command and 'metadata_expire=-1 install --downloadonly' in command
  switch.append({'case':c['case'],'serial':next(iter(serials)),'requests':obs,'rpm_requests':r['rpm_requests'],'download':r['download'],'container_command_index':match[0][0],'container_command':match[0][1]})
assert len(cache)==12 and sum(c['reuse_required'] for c in cache)==4 and len(switch)==2
assert all(x['exit_code']==0 and not x['timed_out'] and not x['output_limit'] for x in D['commands'] if x['stage'] in ['cache-arm','cache-inventory','cache-remove'])
assert len([x for x in D['commands'] if x['stage']=='cache-arm'])==12
logs={n:(gzip.decompress((P/n).read_bytes()).decode() if n.endswith('.gz') else (P/n).read_text()) for n in ['pages-job.log','legacy-native-job.log.gz','stable-job.log']}
assert all(M['sha'] in t for t in logs.values())
assert 'Ran 581 tests' in logs['pages-job.log'] and 'Ran 4 tests' in logs['pages-job.log'] and 'skipped=' not in logs['pages-job.log']
assert 'native-package signed repository container validation passed' in logs['legacy-native-job.log.gz']
print(json.dumps({'receipt_audit_passed':True,'all_observed_ci_passed':True,'head':H,'tree':T,'synthetic_checkout':M['sha'],'legacy':legacy,'baseline':baseline,'cases':49,'rpm_switch':switch,'cache':cache,'cleanup':{'reader':D['reader_cleanup'],'final':D['cleanup'],'caches':D['cache_cleanup']},'limitations':['TEST ONLY; no live/public-origin/source-provenance acceptance.','No original repomd bytes retained; receipt/HTTP digest closure only.','No global CDN atomicity claim.']},indent=2))
