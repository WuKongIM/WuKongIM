#!/usr/bin/env python3
import pathlib,json,subprocess,concurrent.futures
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1030-ci-generation-1');P=sorted((R/'polls').iterdir())[-1]
runs=[json.loads(p.read_text()) for p in P.glob('run-*.json')];assert len(runs)==4 and all(r['status']=='completed' for r in runs)
def call(args):
 r=subprocess.run(args,capture_output=True,timeout=50);assert r.returncode==0,r.stderr[:300];assert 0<len(r.stdout)<1<<20;return json.loads(r.stdout)
def workflow(name):
 return name,call(['gh','api','--method','GET','repos/WuKongIM/WuKongIM/actions/workflows/'+name])
with concurrent.futures.ThreadPoolExecutor(max_workers=3) as ex:
 check=ex.submit(call,['gh','pr','view','1030','--repo','WuKongIM/WuKongIM','--json','headRefOid,baseRefOid,isDraft,mergeStateStatus,statusCheckRollup'])
 states=dict(ex.map(workflow,['review-agent.yml','review-agent-run.yml']))
 checks=check.result()
(R/'final-pr-checks.json').write_text(json.dumps(checks,indent=2)+'\n')
(R/'review-workflow-states.json').write_text(json.dumps(states,indent=2)+'\n')
print(json.dumps({'head':checks['headRefOid'],'draft':checks['isDraft'],'checks':len(checks['statusCheckRollup']),'review_workflows':{n:d['state'] for n,d in states.items()}}))
