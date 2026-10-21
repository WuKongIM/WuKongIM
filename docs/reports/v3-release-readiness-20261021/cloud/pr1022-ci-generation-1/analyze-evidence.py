from pathlib import Path
import json, re, collections
p=Path(__file__).resolve().parent
head='9a4900dc67ac674791edba11ea9a719cd91ec570'; merge='8e8c0ab3321affc8655fb720a308e50bfaca15ca'; base='aeb45c2164b6a50214928f520edd2a8815b7a976'; tree='9a199e729569efa88fed09ee9025c5720f1dbe55'
def read(f): return json.loads(f.read_text())
assert read(p/'head-git-commit.json')['tree']['sha']==tree==read(p/'merge-git-commit.json')['tree']['sha']
summary={'head':head,'base':base,'actual_checkout':merge,'tree':tree,'attempt':1,'candidate_passed':False,'release_ready':False,'failure':read(p/'first-failure.json'),'local_scripts_timeout_is_retained':True,'candidate':{}}
for label,aid in [('default',12357915885),('scripts',12357946122),('e2e',12356928133)]:
 d=p/'artifacts-public'/str(aid)/'members'; ident=read(d/'identity.json'); assert ident['source_sha']==merge and ident['run_attempt']=='1'
 if label!='e2e': assert ident['policy_sha']==base
 results={}
 for f in d.rglob('result.json'):
  r=read(f)
  if r['status']!='not_run':
   assert not r['output_truncated'] and r['cleanup']['verified'] and r['cleanup']['complete'] and not r['cleanup']['remaining_processes']
  results[str(f.relative_to(d))]=r
 summary['candidate'][label]={'artifact_id':aid,'identity':ident,'results':results}
 if label!='e2e':
  policy=read(d/'policy.json'); command=read(d/'command.json'); definition=policy['trusted_checks']['go-unit' if label=='default' else 'scripts-integration']; assert all(command[k]==definition[k] for k in ['arguments','timeout_seconds','max_output_bytes']); summary['candidate'][label]['command']=command
 if label=='default':
  text=(d/'stdout-stderr.log').read_text();summary['candidate'][label]['tested_packages']=len(re.findall(r'^ok\s',text,re.M));summary['candidate'][label]['packages_without_tests']=len(re.findall(r'^\?\s',text,re.M))
 if label=='scripts':summary['candidate'][label]['tests']=read(p/'linux-downward-budget-tests.json')
 if label=='e2e':
  ev=read(d.parent/'test-events.json'); summary['candidate'][label]['test_events']=ev; summary['candidate'][label]['binary_sha256']=(d/'binary.sha256').read_text().split()[0]
  passes=[x['Test'] for x in ev if x['Action']=='pass'];fails=[x['Test'] for x in ev if x['Action']=='fail'];assert len(passes)==4 and len(fails)==2;summary['candidate'][label]['passed_topologies']=passes
summary['performance']=read(p/'performance-summary.json')
d=p/'large-artifact-selected/artifact-12357161437-selected/artifacts';local=read(d/'local-step.json');final=read(d/'report/final.json');qual=read(d/'report/qualification.json');assert local['outcome']=='clean' and final['final']
summary['three_node_correctness']={'local_step':local,'final_messages':final['messages'],'final_harness':final['harness'],'final_queues':final['queues'],'final_correlation':final['correlation'],'measurement_sends':final['messages']['sent']-qual['messages']['sent'],'artifact_boundary':read(p/'large-artifact-selected/public-selected-receipt.json')}
# Read latest API observation for every original run, retaining each prior snapshot.
runs={};jobs={};artifacts={}
for snap in sorted((p/'snapshots').iterdir()):
 for f in snap.glob('*-run.json'):
  j=read(f);runs[j['id']]=j
 for f in snap.glob('*-jobs.json'):
  for j in read(f)['jobs']:jobs[j['id']]=j
 for f in snap.glob('*-artifacts.json'):
  for a in read(f)['artifacts']:artifacts[a['id']]=a
matrix={'head':head,'base':base,'checkout':merge,'tree':tree,'runs':[{k:r.get(k) for k in ['id','name','event','status','conclusion','head_sha','run_attempt','created_at','updated_at','html_url']} for r in runs.values()],'jobs':[],'artifacts':[{'id':a['id'],'name':a['name'],'size_in_bytes':a['size_in_bytes'],'digest':a['digest']} for a in artifacts.values()]}
for j in jobs.values():
 row={k:j.get(k) for k in ['id','run_id','name','head_sha','run_attempt','status','conclusion','started_at','completed_at','html_url','steps']};log=p/'job-logs-public'/str(j['id'])/'complete.log'
 if log.exists():
  s=log.read_text();row['checkout_sha_occurs_in_complete_log']=merge in s
 matrix['jobs'].append(row)
summary['workflows']=matrix['runs'];summary['manager_not_triggered']='Changed 27-file diff does not match Manager path filters; no current-head Manager evidence.';summary['publication_boundary']='Credential-free PR validation only; no signed public package publication, 2h rehearsal, 72h soak, same-data capacity, or 30m recovery claim.'
(p/'evidence-analysis.json').write_text(json.dumps(summary,indent=2)+'\n');(p/'terminal-ci-matrix.json').write_text(json.dumps(matrix,indent=2)+'\n')
print('Analyzed candidate failure, successful scripts/default cleanup, three-node and perf; API terminal states retained separately.')
