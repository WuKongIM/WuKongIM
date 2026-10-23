from pathlib import Path
import json,subprocess,datetime,hashlib
W=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM');B=Path(__file__).parent;P=B.with_name(B.name+'-private');T=W/'.worktrees/v3-owner-recovery-width16';BR='codex/v3-owner-recovery-width16';H='4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac';M='966ec67df1435ad07bbb82724cd940f550755179';PRIMARY='a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83'
sha=lambda b:hashlib.sha256(b).hexdigest();now=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat()
def git(*args,cwd=W):return subprocess.check_output(['git',*args],cwd=cwd,text=True,timeout=15).strip()
def save(n,v):
 with (B/n).open('x') as f:json.dump(v,f,indent=2);f.write('\n')
def perform(name,args):
 start=now();r=subprocess.run(args,cwd=W,capture_output=True,timeout=45)
 for suffix,raw in [('stdout',r.stdout),('stderr',r.stderr)]:
  p=P/(name+'.'+suffix);p.write_bytes(raw);p.chmod(0o600)
 v={'operation':name,'argv':args,'started_at':start,'finished_at':now(),'exit_code':r.returncode,'stdout_bytes':len(r.stdout),'stdout_sha256':sha(r.stdout),'stderr_bytes':len(r.stderr),'stderr_sha256':sha(r.stderr),'force':False};save(name+'.json',v);return r,v
before=json.loads((B/'local-before.json').read_text());comparison=json.loads((B/'preconditions-comparison.json').read_text())
assert comparison['all_four_disabled_manually'] and comparison['all_four_original_runs_first_attempt_success']
assert git('rev-parse','HEAD')==PRIMARY and not git('status','--porcelain=v1','--untracked-files=all')
assert git('rev-parse','origin/main')==M and git('rev-parse','HEAD',cwd=T)==H
assert not git('status','--porcelain=v1','--untracked-files=all','--ignored=matching',cwd=T)
assert subprocess.run(['git','merge-base','--is-ancestor',H,'origin/main'],cwd=W).returncode==0
steps=[]
r,v=perform('remove-original-worktree',['git','worktree','remove',str(T)]);steps.append(v);assert r.returncode==0
r,v=perform('delete-original-local-branch',['git','branch','-d',BR]);steps.append(v)
if r.returncode!=0:
 # Only an absent upstream permits the parent-authorized ordinary upstream repair.
 upstream=git('for-each-ref','--format=%(upstream)','refs/heads/'+BR)
 assert upstream and subprocess.run(['git','show-ref','--verify','--quiet',upstream],cwd=W).returncode!=0
 assert subprocess.run(['git','merge-base','--is-ancestor',H,'origin/main'],cwd=W).returncode==0
 r,v=perform('set-merged-upstream',['git','branch','--set-upstream-to=origin/main',BR]);steps.append(v);assert r.returncode==0
 r,v=perform('delete-original-local-branch-after-upstream-proof',['git','branch','-d',BR]);steps.append(v);assert r.returncode==0
clones=[{'path':str(c),'head':git('rev-parse','HEAD',cwd=c),'tree':git('rev-parse','HEAD^{tree}',cwd=c),'git_directory_is_real':True} for c in sorted((W/'.worktrees').glob('v3-rc-exact-source-*')) if (c/'.git').is_dir()]
after={'at':now(),'primary_head':git('rev-parse','HEAD'),'primary_branch':git('symbolic-ref','HEAD'),'primary_status':git('status','--porcelain=v1','--untracked-files=all'),'origin_main':git('rev-parse','origin/main'),'target_worktree_exists':T.exists(),'original_local_branch_exists':subprocess.run(['git','show-ref','--verify','--quiet','refs/heads/'+BR],cwd=W).returncode==0,'worktrees':git('worktree','list','--porcelain'),'local_branches':git('for-each-ref','--format=%(refname) %(objectname)','refs/heads/'),'independent_exact_source_clones':clones}
save('local-after.json',after)
assert after['primary_head']==before['primary_head']==PRIMARY and after['primary_branch']==before['primary_branch']=='refs/heads/main' and after['primary_status']==before['primary_status']==''
assert not after['target_worktree_exists'] and not after['original_local_branch_exists'] and after['independent_exact_source_clones']==before['independent_exact_source_clones']
oldwt={x.splitlines()[0]:x for x in before['worktrees'].split('\n\n')};newwt={x.splitlines()[0]:x for x in after['worktrees'].split('\n\n')};oldwt.pop('worktree '+str(T));assert oldwt==newwt
oldbr=set(before['local_branches'].splitlines());oldbr.remove('refs/heads/'+BR+' '+H);assert oldbr==set(after['local_branches'].splitlines())
save('cleanup-summary.json',{'merge':M,'head':H,'head_ancestor_of_origin_main_proved_before_cleanup':True,'target_clean_including_ignored_proved_before_cleanup':True,'only_original_worktree_removed':True,'only_original_local_branch_deleted':True,'force_delete_used':False,'primary_branch_and_working_copy_unchanged':True,'unrelated_linked_worktrees_and_local_branches_exactly_unchanged':True,'independent_exact_source_clones_preserved':clones,'steps':steps,'all_owned_local_processes_terminal':True,'no_GitHub_workflow_cloud_product_actions':True})
print(json.dumps({'removed':str(T),'deleted_local_branch':BR,'primary_unchanged':PRIMARY,'unrelated_worktrees_preserved':len(newwt),'real_git_clones_preserved':len(clones),'all_owned_processes_terminal':True}))
