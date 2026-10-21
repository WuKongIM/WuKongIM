#!/usr/bin/env python3
import datetime,hashlib,json,os,pathlib,subprocess,time
root=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job')
out=pathlib.Path('/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis')
env=dict(os.environ,LC_ALL='C',LANG='C',GOTOOLCHAIN='go1.25.11',GOWORK='off',GOFLAGS='-p=2',GOMAXPROCS='2',GOCACHE=str(out/'gocache'))
results=[]
for label in ['bash3','bash5']:
 env['PATH']=str(out/label/'bin')+os.pathsep+os.environ['PATH']
 cmd=['go','test','-tags=integration','./scripts','-run','^TestChatLifecycle(PreClock|StageServiceState)','-count=1','-json','-timeout=90s']
 prefix=out/('related-'+label);assert not prefix.with_suffix('.jsonl').exists()
 start=time.monotonic()
 with prefix.with_suffix('.jsonl').open('wb') as stdout,prefix.with_suffix('.stderr').open('wb') as stderr:
  run=subprocess.run(cmd,cwd=root,env=env,stdout=stdout,stderr=stderr,timeout=180)
 events=[json.loads(line) for line in prefix.with_suffix('.jsonl').read_text().splitlines()]
 rec={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'command':cmd,'exit_code':run.returncode,'elapsed_seconds':time.monotonic()-start,'shell':subprocess.check_output(['bash','--version'],env=env,text=True).splitlines()[0],'passed':[e['Test'] for e in events if e.get('Action')=='pass' and 'Test' in e],'failed':[e['Test'] for e in events if e.get('Action')=='fail' and 'Test' in e],'skipped':[e['Test'] for e in events if e.get('Action')=='skip' and 'Test' in e]}
 prefix.with_suffix('.receipt.json').write_text(json.dumps(rec,indent=2)+'\n');results.append(rec)
 print(label,'related exit',run.returncode,'pass',len(rec['passed']),'fail',len(rec['failed']),'skip',len(rec['skipped']),flush=True)
policy=json.loads((root/'.github/review-agent/policy.json').read_bytes());check=policy['trusted_checks']['flow-doc-contracts']
p=out/'flow-doc-contracts.log';start=time.monotonic()
with p.open('wb') as output:
 run=subprocess.run(check['arguments'],cwd=root/check['working_dir'],env=env,stdout=output,stderr=subprocess.STDOUT,timeout=check['timeout_seconds'])
assert p.stat().st_size<=check['max_output_bytes']
(out/'flow-doc-contracts.receipt.json').write_text(json.dumps({'named_check':'flow-doc-contracts','policy_sha256':hashlib.sha256((root/'.github/review-agent/policy.json').read_bytes()).hexdigest(),'contract':check,'exit_code':run.returncode,'elapsed_seconds':time.monotonic()-start,'output_sha256':hashlib.sha256(p.read_bytes()).hexdigest()},indent=2)+'\n')
print('flow-doc-contracts',run.returncode,flush=True)
