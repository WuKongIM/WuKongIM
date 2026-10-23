from pathlib import Path
import json,subprocess,datetime,hashlib,os
W=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM');B=Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1031-merge-proof-v1');P=B.with_name(B.name+'-private');T=W/'.worktrees/v3-metrics-parse-leaf-diagnostics';C=W/'.worktrees/v3-rc-exact-source-2662-parse-leaf';BR='codex/v3-metrics-parse-leaf-diagnostics';H='2662fe93b754d614422f2936185cf781a33b831a';M='364253b3be532acb023757791a54b85d4c76cbd2';PRIMARY='a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83'
sha=lambda b:hashlib.sha256(b).hexdigest();now=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat()
def git(args,cwd=W):return subprocess.check_output(['git',*args],cwd=cwd,text=True).strip()
def save(n,v):
 with (B/n).open('x') as f:json.dump(v,f,indent=2);f.write('\n')
before=json.loads((B/'local-before.json').read_text());assert json.loads((B/'preconditions-comparison.json').read_text())['all_four_disabled_manually']
assert git(['rev-parse','HEAD'])==PRIMARY and not git(['status','--porcelain=v1','--untracked-files=all']);assert git(['rev-parse','origin/main'])==M
assert git(['rev-parse','HEAD'],T)==H and not git(['status','--porcelain=v1','--untracked-files=all','--ignored=matching'],T);assert subprocess.run(['git','merge-base','--is-ancestor',H,'origin/main'],cwd=W).returncode==0
steps=[]
for name,args in [('remove-original-worktree',['git','worktree','remove',str(T)]),('delete-original-local-branch',['git','branch','-d',BR])]:
 start=now();r=subprocess.run(args,cwd=W,capture_output=True,timeout=45)
 for suffix,data in [('stdout',r.stdout),('stderr',r.stderr)]:
  p=P/(name+'.'+suffix);p.write_bytes(data);p.chmod(0o600)
 item={'operation':name,'argv':args,'started_at':start,'finished_at':now(),'exit_code':r.returncode,'stdout_bytes':len(r.stdout),'stdout_sha256':sha(r.stdout),'stderr_bytes':len(r.stderr),'stderr_sha256':sha(r.stderr),'force':False};save(name+'.json',item);steps.append(item);assert r.returncode==0
current={'at':now(),'primary_head':git(['rev-parse','HEAD']),'primary_branch':git(['symbolic-ref','HEAD']),'primary_status':git(['status','--porcelain=v1','--untracked-files=all']),'origin_main':git(['rev-parse','origin/main']),'target_worktree_exists':T.exists(),'original_local_branch_exists':subprocess.run(['git','show-ref','--verify','--quiet','refs/heads/'+BR],cwd=W).returncode==0,'worktrees':git(['worktree','list','--porcelain']),'local_branches':git(['for-each-ref','--format=%(refname) %(objectname)','refs/heads/']),'exact_source_clone':{'path':str(C),'head':git(['rev-parse','HEAD'],C),'tree':git(['rev-parse','HEAD^{tree}'],C),'git_dir':git(['rev-parse','--absolute-git-dir'],C)}}
save('local-after.json',current)
assert current['primary_head']==before['primary_head']==PRIMARY and current['primary_branch']==before['primary_branch']=='refs/heads/main' and current['primary_status']==before['primary_status']==''
assert not current['target_worktree_exists'] and not current['original_local_branch_exists'] and current['exact_source_clone']==before['exact_source_clone']
oldwt={x.splitlines()[0]:x for x in before['worktrees'].split('\n\n')};newwt={x.splitlines()[0]:x for x in current['worktrees'].split('\n\n')};oldwt.pop('worktree '+str(T));assert oldwt==newwt
oldbr=set(before['local_branches'].splitlines());oldbr.remove('refs/heads/'+BR+' '+H);assert oldbr==set(current['local_branches'].splitlines())
save('cleanup-summary.json',{'merge':M,'head':H,'tree':current['exact_source_clone']['tree'],'head_ancestor_of_origin_main_proved_before_cleanup':True,'target_clean_including_ignored_proved_before_cleanup':True,'only_original_worktree_removed':True,'only_original_local_branch_deleted':True,'force_delete_used':False,'primary_branch_and_working_copy_unchanged':True,'unrelated_linked_worktrees_and_local_branches_exactly_unchanged':True,'exact_source_real_git_clone_preserved':current['exact_source_clone'],'steps':steps,'all_owned_local_processes_terminal':True,'no_GitHub_workflow_cloud_product_actions':True})
print(json.dumps({'removed':str(T),'deleted_local_branch':BR,'primary_unchanged':PRIMARY,'exact_clone_preserved':str(C),'unrelated_worktrees_preserved':len(newwt),'all_owned_processes_terminal':True}))
