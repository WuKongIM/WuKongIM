from pathlib import Path
import json,subprocess,hashlib,datetime,os,concurrent.futures
B=Path(__file__).parent;P=B.with_name(B.name+'-private');P.mkdir(mode=0o700,exist_ok=False)
W=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM');T=W/'.worktrees/v3-owner-recovery-width16';PRE=B.with_name('pr1033-ordinary-merge-preconditions-v1')
H='4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac';BASE='9e5d8eeadb7b4c1998857640d2160c3af3232996';M='966ec67df1435ad07bbb82724cd940f550755179';TREE='1f2826553a264053e954229ac2581bcf48afe6ad';PRIMARY='a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83'
sha=lambda b:hashlib.sha256(b).hexdigest();now=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat()
def save(n,v):
 with (B/n).open('x') as f:json.dump(v,f,indent=2);f.write('\n')
def git(*args,cwd=W):return subprocess.check_output(['git',*args],cwd=cwd,text=True,timeout=15).strip()
def clones():return [{'path':str(c),'head':git('rev-parse','HEAD',cwd=c),'tree':git('rev-parse','HEAD^{tree}',cwd=c),'git_directory_is_real':True} for c in sorted((W/'.worktrees').glob('v3-rc-exact-source-*')) if (c/'.git').is_dir()]
before={'at':now(),'primary_head':git('rev-parse','HEAD'),'primary_branch':git('symbolic-ref','HEAD'),'primary_status':git('status','--porcelain=v1','--untracked-files=all'),'origin_main_before_fetch':git('rev-parse','origin/main'),'target_path':str(T),'target_head':git('rev-parse','HEAD',cwd=T),'target_tree':git('rev-parse','HEAD^{tree}',cwd=T),'target_branch':git('symbolic-ref','HEAD',cwd=T),'target_status_including_ignored':git('status','--porcelain=v1','--untracked-files=all','--ignored=matching',cwd=T),'worktrees':git('worktree','list','--porcelain'),'local_branches':git('for-each-ref','--format=%(refname) %(objectname)','refs/heads/'),'independent_exact_source_clones':clones()}
save('local-before.json',before)
assert before['primary_head']==PRIMARY and before['primary_branch']=='refs/heads/main' and not before['primary_status']
assert before['target_head']==H and before['target_tree']==TREE and not before['target_status_including_ignored']
proof=[]
assert sha((PRE/'SHA256SUMS').read_bytes())=='005e9385ad92657bea27ec2d1cd91080f57724741bf3408e45b3223392e0b2b2'
for line in (PRE/'SHA256SUMS').read_text().splitlines():
 h,n=line.split('  ',1);raw=(PRE/n).read_bytes();assert sha(raw)==h;proof.append({'path':n,'sha256':h,'bytes':len(raw)})
start=now();r=subprocess.run(['git','fetch','--no-tags','origin','refs/heads/main:refs/remotes/origin/main'],cwd=W,capture_output=True,timeout=55)
for suffix,raw in [('stdout',r.stdout),('stderr',r.stderr)]:
 p=P/('fetch.'+suffix);p.write_bytes(raw);p.chmod(0o600)
save('fetch-receipt.json',{'started_at':start,'finished_at':now(),'argv':['git','fetch','--no-tags','origin','refs/heads/main:refs/remotes/origin/main'],'exit_code':r.returncode,'stdout_bytes':len(r.stdout),'stdout_sha256':sha(r.stdout),'stderr_bytes':len(r.stderr),'stderr_sha256':sha(r.stderr),'primary_branch_updated':False});assert r.returncode==0
assert git('rev-parse','origin/main')==M
parents=git('show','-s','--format=%P',M).split();assert parents==[BASE,H]
assert git('rev-parse',M+'^{tree}')==TREE==git('rev-parse',H+'^{tree}')
assert subprocess.run(['git','merge-base','--is-ancestor',H,'origin/main'],cwd=W).returncode==0
save('merge-git-proof.json',{'merge':M,'parents':parents,'origin_main':M,'head':H,'tree':TREE,'head_tree_equal':True,'head_is_ancestor_of_origin_main':True,'primary_head_after_fetch':git('rev-parse','HEAD'),'primary_status_after_fetch':git('status','--porcelain=v1','--untracked-files=all')})
keep=lambda v,ks:{k:v.get(k) for k in ks}
def get(item):
 name,endpoint,project=item;start=now();r=subprocess.run(['gh','api','--method','GET','repos/WuKongIM/WuKongIM/'+endpoint],capture_output=True,timeout=55)
 for suffix,raw in [('stdout',r.stdout),('stderr',r.stderr)]:
  p=P/(name+'.'+suffix);p.write_bytes(raw);p.chmod(0o600)
 save(name+'.read.json',{'method':'GET','endpoint':endpoint,'started_at':start,'finished_at':now(),'exit_code':r.returncode,'stdout_bytes':len(r.stdout),'stdout_sha256':sha(r.stdout),'stderr_bytes':len(r.stderr),'stderr_sha256':sha(r.stderr),'raw_private_only':True});assert r.returncode==0 and len(r.stdout)<=4<<20
 v=project(json.loads(r.stdout));save(name+'.json',v);return name,v
ids={324186999,324187001,324187004,324248914}
def workflows(v):
 assert v['total_count']<=100
 rows=[keep(x,['id','name','path','state','created_at','updated_at']) for x in v['workflows'] if x['id'] in ids];assert len(rows)==4;return rows
def pr(v):return keep(v,['number','state','merged','merged_at','merge_commit_sha','html_url','body'])|{'head':v['head']['sha'],'base':v['base']['sha']}
def checks(v):
 assert v['total_count']<=100
 return {'total_count':v['total_count'],'check_runs':[keep(x,['id','name','head_sha','status','conclusion','started_at','completed_at','html_url','details_url']) for x in v['check_runs']]}
runids=[39893010352,39893010250,39893010300,39893010410]
items=[('pr-after','pulls/1033',pr),('main-protection-after','branches/main/protection',lambda v:v),('rulesets-after','rulesets?per_page=100',lambda v:v),('workflows-after','actions/workflows?per_page=100',workflows),('checks-after','commits/'+H+'/check-runs?per_page=100',checks)]
items += [('run-'+str(i),'actions/runs/'+str(i),lambda v:keep(v,['id','name','head_sha','head_branch','event','run_attempt','status','conclusion','created_at','updated_at','html_url'])) for i in runids]
with concurrent.futures.ThreadPoolExecutor(max_workers=5) as ex:values=dict(ex.map(get,items))
p=values['pr-after'];assert p['merged'] and p['merge_commit_sha']==M and p['head']==H and p['base']==BASE and p['merged_at']=='2026-10-23T03:31:17Z'
bodyfile=Path('/tmp/wukongim-v3-readiness-20261023/width16-pr-body.md');body=bodyfile.read_bytes();assert sha(body)=='461f4de16ec2441fad1da5da6a4c3617993f2b50db5c46feff6a24ed48f2644d' and p['body'].encode()==body
save('final-body-proof.json',{'local_body_path':str(bodyfile),'body_bytes':len(body),'local_sha256':sha(body),'remote_sha256':sha(p['body'].encode()),'exact_bytes_equal':True,'ordinary_exact_head_merge':'Parent reported successful gh ordinary exact-head merge; two-parent Git merge and REST merged identity independently verified. No admin override reported or performed here.'})
assert values['main-protection-after']==json.loads((PRE/'main-protection-before.json').read_text()) and values['rulesets-after']==json.loads((PRE/'rulesets-before.json').read_text())
assert sorted(values['workflows-after'],key=lambda x:x['id'])==sorted(json.loads((PRE/'workflows-before.json').read_text()),key=lambda x:x['id']) and {x['state'] for x in values['workflows-after']}=={'disabled_manually'}
for i in runids:
 r=values['run-'+str(i)];assert r['head_sha']==H and r['event']=='pull_request' and r['run_attempt']==1 and r['status']=='completed' and r['conclusion']=='success'
ci=B.with_name('pr1033-ci-generation-1');validation=json.loads((ci/'validation.json').read_text());assert sha((ci/'SHA256SUMS').read_bytes())=='923350d31ae94f38cc2fb0f63c05908ba87e8bc80b58cd6387080c043632fd8a'
oldpoll=sorted((ci/'polls').iterdir())[-1];jobids={j['id'] for f in oldpoll.glob('jobs-*.json') if not f.name.endswith('.read.json') for j in json.loads(f.read_text())['jobs']};assert len(jobids)==18
current=values['checks-after']['check_runs'];bound=[x for x in current if x['id'] in jobids];assert len(bound)==18 and all(x['head_sha']==H and x['status']=='completed' and x['conclusion'] in ['success','skipped'] for x in bound)
source=[]
for rel in ['AGENTS.md','.github/workflows','.github/review-agent','.github/issue-agent','.agents/skill-tests.json','docs/agents']:
 old=git('rev-parse',BASE+':'+rel);merged=git('rev-parse',M+':'+rel);assert old==merged;source.append({'path':rel,'base_object':old,'merge_object':merged,'identical':True})
absent={}
for rev in [BASE,H,M]:
 q=subprocess.run(['git','cat-file','-e',rev+':.github/workflows/review-agent-userns-diagnostic.yaml'],cwd=W,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL);absent[rev]=q.returncode!=0
assert all(absent.values())
save('preconditions-comparison.json',{'preconditions_directory':str(PRE),'preconditions_manifest_sha256':sha((PRE/'SHA256SUMS').read_bytes()),'preconditions_files_verified':proof,'main_protection_structurally_identical':True,'rulesets_structurally_identical':True,'four_review_agent_metadata_identical':True,'all_four_disabled_manually':True,'approved_exact_head_base_and_body_match':True,'protected_objects':source,'historical_userns_api_path_absent_in_base_head_merge':absent,'original_18_first_attempt_checks_preserved':bound,'additional_current_checks':[x for x in current if x['id'] not in jobids],'all_four_original_runs_first_attempt_success':True,'no_product_workflow_cloud_execution':True})
print(json.dumps({'merge':M,'tree':TREE,'parents':parents,'ancestry':True,'original_first_checks':len(bound),'additional_checks':[{k:x[k] for k in ['id','name','status','conclusion']} for x in current if x['id'] not in jobids],'final_body_sha256':sha(body),'protections_unchanged':True,'ready_for_authorized_clean_only_cleanup':True}))
