from pathlib import Path
import subprocess,json,hashlib,datetime,os,concurrent.futures
W=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM');B=Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1031-merge-proof-v1');B.mkdir(exist_ok=False);P=B.with_name(B.name+'-private');P.mkdir(mode=0o700)
BASE='8dfcd35b7d604fb5d3da3d75e36ab267e2f4b1bf';H='2662fe93b754d614422f2936185cf781a33b831a';M='364253b3be532acb023757791a54b85d4c76cbd2';PRIMARY='a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83';TREE='1182b14d5b659eb60f2e7b694ff91b6641189106';T=W/'.worktrees/v3-metrics-parse-leaf-diagnostics';C=W/'.worktrees/v3-rc-exact-source-2662-parse-leaf'
sha=lambda b:hashlib.sha256(b).hexdigest()
now=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat()
def save(n,v):
 with (B/n).open('x') as f:json.dump(v,f,indent=2);f.write('\n')
def git(args,cwd=W):return subprocess.check_output(['git',*args],cwd=cwd,text=True).strip()
before={'at':now(),'primary_head':git(['rev-parse','HEAD']),'primary_branch':git(['symbolic-ref','HEAD']),'primary_status':git(['status','--porcelain=v1','--untracked-files=all']),'target_path':str(T),'target_head':git(['rev-parse','HEAD'],T),'target_branch':git(['symbolic-ref','HEAD'],T),'target_status_including_ignored':git(['status','--porcelain=v1','--untracked-files=all','--ignored=matching'],T),'worktrees':git(['worktree','list','--porcelain']),'local_branches':git(['for-each-ref','--format=%(refname) %(objectname)','refs/heads/']),'exact_source_clone':{'path':str(C),'head':git(['rev-parse','HEAD'],C),'tree':git(['rev-parse','HEAD^{tree}'],C),'git_dir':git(['rev-parse','--absolute-git-dir'],C)}}
assert before['primary_head']==PRIMARY and before['primary_branch']=='refs/heads/main' and not before['primary_status'];assert before['target_head']==H and before['target_branch']=='refs/heads/codex/v3-metrics-parse-leaf-diagnostics' and not before['target_status_including_ignored'];assert before['exact_source_clone']['head']==H
save('local-before.json',before)
start=now();r=subprocess.run(['git','fetch','--no-tags','origin','refs/heads/main:refs/remotes/origin/main'],cwd=W,capture_output=True,timeout=90)
for n,b in [('fetch.stdout',r.stdout),('fetch.stderr',r.stderr)]:
 p=P/n;p.write_bytes(b);p.chmod(0o600)
save('fetch-receipt.json',{'started_at':start,'completed_at':now(),'argv':['git','fetch','--no-tags','origin','refs/heads/main:refs/remotes/origin/main'],'exit_code':r.returncode,'stdout_bytes':len(r.stdout),'stdout_sha256':sha(r.stdout),'stderr_bytes':len(r.stderr),'stderr_sha256':sha(r.stderr),'primary_branch_updated':False});assert r.returncode==0
assert git(['rev-parse','origin/main'])==M
parents=git(['show','-s','--format=%P',M]).split();assert parents==[BASE,H];assert git(['rev-parse',M+'^{tree}'])==TREE==git(['rev-parse',H+'^{tree}']);assert subprocess.run(['git','merge-base','--is-ancestor',H,'origin/main'],cwd=W).returncode==0
save('merge-git-proof.json',{'merge':M,'parents':parents,'origin_main':M,'head':H,'merge_tree':TREE,'head_tree':TREE,'head_is_ancestor_of_origin_main':True,'primary_head_after_fetch':git(['rev-parse','HEAD']),'primary_status_after_fetch':git(['status','--porcelain=v1','--untracked-files=all'])})
def get(item):
 name,path=item;start=now();r=subprocess.run(['gh','api','--method','GET','repos/WuKongIM/WuKongIM/'+path],capture_output=True,timeout=55)
 for suffix,b in [('stdout',r.stdout),('stderr',r.stderr)]:
  p=P/(name+'.'+suffix);p.write_bytes(b);p.chmod(0o600)
 save(name+'.read.json',{'method':'GET','endpoint':path,'started_at':start,'finished_at':now(),'exit_code':r.returncode,'stdout_bytes':len(r.stdout),'stdout_sha256':sha(r.stdout),'stderr_bytes':len(r.stderr),'stderr_sha256':sha(r.stderr)});assert r.returncode==0
 v=json.loads(r.stdout)
 if name=='pr-after':v={k:v.get(k) for k in ['number','state','merged','merged_at','merge_commit_sha','html_url']}|{'head':v['head']['sha'],'base':v['base']['sha']}
 if name=='workflows-after':
  assert v['total_count']<=100;v={'total_count':v['total_count'],'review_agent_workflows':[{k:x.get(k) for k in ['id','name','path','state','created_at','updated_at']} for x in v['workflows'] if 'review-agent' in x['path']]}
 save(name+'.json',v);return name,v
items=[('pr-after','pulls/1031'),('main-protection-after','branches/main/protection'),('rulesets-after','rulesets?per_page=100'),('workflows-after','actions/workflows?per_page=100')]
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:vals=dict(pool.map(get,items))
pre=Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1031-ordinary-merge-preconditions-v1');proof=[]
for line in (pre/'SHA256SUMS').read_text().splitlines():
 h,n=line.split('  ',1);b=(pre/n).read_bytes();assert sha(b)==h;proof.append({'path':n,'bytes':len(b),'sha256':h})
assert vals['pr-after']['merged'] is True and vals['pr-after']['merge_commit_sha']==M and vals['pr-after']['head']==H and vals['pr-after']['base']==BASE
assert vals['main-protection-after']==json.loads((pre/'main-protection-before.json').read_text());assert vals['rulesets-after']==json.loads((pre/'rulesets-before.json').read_text())
old=json.loads((pre/'workflows-before.json').read_text());old=[{k:x.get(k) for k in ['id','name','path','state','created_at','updated_at']} for x in old['workflows'] if 'review-agent' in x['path']];new=vals['workflows-after']['review_agent_workflows'];assert len(new)==4 and {x['state'] for x in new}=={'disabled_manually'} and sorted(old,key=lambda x:x['id'])==sorted(new,key=lambda x:x['id'])
save('preconditions-comparison.json',{'preconditions_directory':str(pre),'preconditions_manifest_sha256':sha((pre/'SHA256SUMS').read_bytes()),'preconditions_files_verified':proof,'main_protection_structurally_identical':True,'rulesets_structurally_identical':True,'four_review_agent_workflow_states_and_metadata_identical':True,'all_four_disabled_manually':True,'approved_exact_head_and_base_match':True})
print(json.dumps({'output':str(B),'merge':M,'tree':TREE,'parents':parents,'head_ancestor':True,'protections_unchanged':True,'four_review_agent_unchanged':True,'target_clean_including_ignored':True,'exact_source_clone_preserved':str(C),'ready_for_ordinary_cleanup':True}))
