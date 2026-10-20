import datetime,hashlib,json,os,pathlib,subprocess,time
root=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-qualification')
out=pathlib.Path('/tmp/wukongim-migration-repair-20261020')
def git(*args): return subprocess.check_output(['git',*args],cwd=root,text=True).strip()
def sha(path): return hashlib.sha256(path.read_bytes()).hexdigest()
assert git('status','--porcelain')==''
source=git('rev-parse','HEAD'); tree=git('rev-parse','HEAD^{tree}'); binary=out/'wukongim-e2e'
build=subprocess.check_output(['go','version','-m',str(binary)],text=True)
assert 'vcs.revision='+source in build and 'vcs.modified=false' in build
(out/'binary-build-info.txt').write_text(build)
env={k:v for k,v in os.environ.items() if not k.startswith('WK_')}
env.update({'GOWORK':'off','GIT_DIR':git('rev-parse','--absolute-git-dir'),'GIT_WORK_TREE':str(root),'LC_ALL':'C','LANG':'C','LC_CTYPE':'C','WK_E2E_BINARY':str(binary),'WK_E2E_SOURCE_REVISION':source,'WK_E2E_SOURCE_FINGERPRINT':tree})
receipt={'source_revision':source,'source_tree':tree,'source_clean':True,'binary_sha256':sha(binary),'go_version':subprocess.check_output(['go','version'],text=True).strip(),'cases':[],'status':'running','instructions_sha256':{p:sha(root/p) for p in git('ls-files','AGENTS.md','**/AGENTS.md','**/FLOW.md').splitlines()}}
plan=[('migration-1-to-3','./test/e2e/migration/ordinary_history','12m',['-run','^TestOriginalOrdinaryHistoryTopology$/^1_source_nodes_to_3_target_nodes$'],{}),('migration-all-topologies','./test/e2e/migration/ordinary_history','12m',[],{}),('original-v2-controls','./test/e2e/migration/original_v2','5m',[],{}),('cold-hot','./test/e2e/message/cold_hot_send','4m',[],{'WK_E2E_COLD_HOT_REPORT':str(out/'cold-hot.json')}),('channel-failover','./test/e2e/message/channel_failover','6m',[],{})]
receipt['declared_plan']=[{'name':n,'package':p,'timeout':t,'extra':a,'env':e} for n,p,t,a,e in plan]
path=out/'regressions.json'
def save(): path.write_text(json.dumps(receipt,indent=2)+'\n')
save()
for name,pkg,bound,args,extras in plan:
 assert git('status','--porcelain')==''
 cmd=['go','test','-tags=e2e',pkg,'-count=1','-timeout='+bound,'-p=1','-parallel=1','-json',*args]
 runenv={**env,**extras}; log=out/(name+'.jsonl')
 c={'name':name,'command':cmd,'environment':{k:v for k,v in runenv.items() if k in ('GOWORK','LC_ALL','GIT_DIR','GIT_WORK_TREE') or k.startswith('WK_E2E_')},'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'running'}
 receipt['cases'].append(c);save();print('START '+name,flush=True);start=time.monotonic()
 with log.open('w') as f:r=subprocess.run(cmd,cwd=root,env=runenv,stdout=f,stderr=subprocess.STDOUT)
 c.update({'exit_code':r.returncode,'status':'pass' if r.returncode==0 else 'fail','elapsed_seconds':round(time.monotonic()-start,3),'finished_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'log':log.name,'log_sha256':sha(log),'test_outcomes':[],'history_observations':[],'source_clean_after':git('status','--porcelain')==''})
 for line in log.read_text().splitlines():
  try:e=json.loads(line)
  except ValueError:continue
  if e.get('Action') in ['pass','fail','skip']: c['test_outcomes'].append({k:e[k] for k in ['Action','Package','Test','Elapsed'] if k in e})
  if 'history verified:' in e.get('Output',''): c['history_observations'].append(e['Output'].strip())
 save();print(name+' '+c['status']+' '+str(c['elapsed_seconds'])+'s',flush=True)
 if r.returncode!=0:receipt['status']='fail';save();raise SystemExit(r.returncode)
receipt['status']='pass';save();print('ALL REGRESSIONS PASS',flush=True)
