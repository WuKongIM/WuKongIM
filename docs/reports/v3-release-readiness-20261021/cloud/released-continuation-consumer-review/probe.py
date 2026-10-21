"""Offline review: actual frozen consumer shell, with authority-only stub."""
from pathlib import Path
import hashlib,json,os,subprocess,tempfile,shutil
import yaml
root=Path(__file__).resolve().parent
repo='/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM'
base='ce3c4d9110f17bb71dc99a89dc3c09758fabf980'

def steps(doc): return doc['jobs']['finalize']['steps']
def pick(ss,key): return next(s for s in ss if s.get('id')==key or s.get('name')==key)
checks=[]
for stage in ['rehearsal','formal']:
 rel=f'.github/workflows/chat-lifecycle-{stage}-finalize.yml'
 current=yaml.safe_load((root/'source'/rel).read_text())
 old=yaml.safe_load(subprocess.check_output(['git','show',base+':'+rel],cwd=repo))
 a,b=steps(current),steps(old)
 stable=['release','cleanup','Delete released encrypted deployment handoff','Upload '+('formal ' if stage=='formal' else '')+'zero-inventory proof','Upload '+('formal' if stage=='formal' else 'rehearsal')+' cleanup-pending continuation']
 for key in stable:
  assert pick(a,key)==pick(b,key),(stage,key)
  checks.append({'stage':stage,'unchanged_step':key})
 assert current.get('permissions')==old.get('permissions')
 assert current.get('concurrency')==old.get('concurrency')
 for key in ['final_upload','complete_upload']:
  assert pick(a,key)['with']['retention-days']==90
 assert a.index(pick(a,'release'))<a.index(pick(a,'cleanup'))<a.index(pick(a,'continuation'))
 assert pick(a,'continuation')['continue-on-error'] is True
 assert a.index(pick(a,'continuation_failure'))>a.index(pick(a,'Delete released encrypted deployment handoff'))

formal=steps(yaml.safe_load((root/'source/.github/workflows/chat-lifecycle-formal-finalize.yml').read_text()))
block=pick(formal,'continuation')['run']
results=[]
for previous in [False,True]:
 for case in ['matching','handoff_prior_dropped','selected_terminal_prior_dropped','authority_rejected']:
  with tempfile.TemporaryDirectory(prefix='consumer-review-') as scratch:
   p=Path(scratch); handoff=p/'handoff'; final=p/'final'; prior=p/'previous-final'; bin_dir=p/'bin'
   for d in [handoff/'rehearsal',final,prior,bin_dir]: d.mkdir(parents=True)
   committed=104477600
   for d in [handoff,final,prior]: (d/'run-plan.json').write_text(json.dumps({'lease_plan':{'budget':{'committed_micros':committed}}}))
   (handoff/'rehearsal/transition.json').write_text(json.dumps({'committed_micros':committed}))
   if case=='handoff_prior_dropped': (handoff/'run-plan.json').write_text('{"lease_plan":{"budget":{"committed_micros":0}}}')
   if case=='selected_terminal_prior_dropped': ((prior if previous else final)/'run-plan.json').write_text('{"lease_plan":{"budget":{"committed_micros":0}}}')
   stub=bin_dir/'python3'; stub.write_text('#!/bin/bash\nexit "$AUTHORITY_STATUS"\n'); stub.chmod(0o700)
   script=block.replace('${{ steps.existing.outputs.ready }}',str(previous).lower())
   env=dict(os.environ,PATH=str(bin_dir)+os.pathsep+os.environ['PATH'],WK_CHAT_HANDOFF_DIR=str(handoff),WK_CHAT_FINAL_DIR=str(final),RUNNER_TEMP=str(p),AUTHORITY_STATUS='19' if case=='authority_rejected' else '0')
   completed=subprocess.run(['/bin/bash','-c',script],env=env,capture_output=True,text=True,timeout=5)
   expected=0 if case=='matching' else 19 if case=='authority_rejected' else 1
   assert completed.returncode==expected,(case,previous,completed)
   results.append({'case':case,'previous_terminal':previous,'expected_exit':expected,'actual_exit':completed.returncode,'stdout':completed.stdout,'stderr':completed.stderr})
(root/'independent-probe-results.json').write_text(json.dumps({'frozen_shell_sha256':hashlib.sha256(block.encode()).hexdigest(),'cleanup_steps_exactly_unchanged':checks,'retention_days_unchanged':90,'permissions_and_concurrency_unchanged':True,'formal_cost_binding_cases':results,'uses_real_bash_and_jq':True,'authority_stubbed':True,'github_or_cloud_called':False},indent=2)+'\n')
print(json.dumps({'checks':len(checks),'shell_cases':len(results),'pass':True}))
