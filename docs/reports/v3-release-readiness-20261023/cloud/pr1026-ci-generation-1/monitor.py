#!/usr/bin/env python3
import concurrent.futures, datetime, hashlib, json, pathlib, subprocess, sys
ROOT=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/pr1026-ci-generation-1')
HEAD='23cf3f8764c2b841128b1a80d79ba9f49f447549'
REPO='repos/WuKongIM/WuKongIM/'
now=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
def api(endpoint):
    p=subprocess.run(['gh','api','--method','GET',REPO+endpoint],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=60)
    if p.returncode:raise RuntimeError({'endpoint':endpoint,'exit':p.returncode,'stderr':p.stderr.decode()[:500]})
    if len(p.stdout)>4<<20:raise RuntimeError('metadata size limit')
    return json.loads(p.stdout)
def save(name,x):
    p=ROOT/name;p.parent.mkdir(exist_ok=True,parents=True);p.write_text(json.dumps(x,indent=2)+'\n');return x
if len(sys.argv)>1 and sys.argv[1]=='initial':
    endpoints={'pr.json':'pulls/1026','head-runs.json':'actions/runs?head_sha='+HEAD+'&per_page=100','head-commit.json':'git/commits/'+HEAD,'issue-signal-run.json':'actions/runs/39832747456'}
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        futures={pool.submit(api,v):k for k,v in endpoints.items()}
        results={name:save(name,f.result()) for f,name in futures.items()}
    assert results['pr.json']['head']['sha']==HEAD
    print(json.dumps({'head_runs':[(x['id'],x['name'],x['status'],x['conclusion']) for x in results['head-runs.json']['workflow_runs']]}))
else:
    ids=[39832747504,39832747798]
    endpoints={f'run-{rid}.json':f'actions/runs/{rid}/attempts/1' for rid in ids}
    endpoints.update({f'jobs-{rid}.json':f'actions/runs/{rid}/attempts/1/jobs?per_page=100' for rid in ids})
    endpoints.update({f'artifacts-{rid}.json':f'actions/runs/{rid}/artifacts?per_page=100' for rid in ids})
    out=ROOT/'polls'/now;out.mkdir(parents=True,exist_ok=False)
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        futures={pool.submit(api,v):k for k,v in endpoints.items()}
        results={name:save(str(out.relative_to(ROOT)/name),f.result()) for f,name in futures.items()}
    for rid in ids:
        run=results[f'run-{rid}.json'];jobs=results[f'jobs-{rid}.json'];arts=results[f'artifacts-{rid}.json']
        assert run['head_sha']==HEAD and run['run_attempt']==1
        assert len(jobs['jobs'])==jobs['total_count']<=100
        assert len(arts['artifacts'])==arts['total_count']<=100
    summary={'polled_at':now,'directory':str(out),'runs':[{'id':rid,'status':results[f'run-{rid}.json']['status'],'conclusion':results[f'run-{rid}.json']['conclusion'],'jobs':[{'id':j['id'],'name':j['name'],'status':j['status'],'conclusion':j['conclusion'],'active_steps':[s['name'] for s in j['steps'] if s['status']=='in_progress']} for j in results[f'jobs-{rid}.json']['jobs']],'artifacts':[{'id':a['id'],'name':a['name'],'size':a['size_in_bytes'],'digest':a.get('digest')} for a in results[f'artifacts-{rid}.json']['artifacts']]} for rid in ids]}
    save(str(out.relative_to(ROOT)/'summary.json'),summary)
    print(json.dumps(summary))
