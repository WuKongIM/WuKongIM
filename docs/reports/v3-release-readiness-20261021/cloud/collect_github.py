#!/usr/bin/env python3
"""GET-only GitHub configuration audit; never request secret contents or cloud APIs."""
import concurrent.futures, datetime, json, pathlib, subprocess
OUT=pathlib.Path(__file__).resolve().parent
REPO='WuKongIM/WuKongIM'
PREFIX='repos/'+REPO+'/'
VARIABLES=['ALIBABA_CLOUD_LEASE_REGION','ALIBABA_CLOUD_LEASE_ACCOUNT_ID_HASH','ALIBABA_CLOUD_LEASE_OIDC_PROVIDER_ARN','ALIBABA_CLOUD_LEASE_OIDC_AUDIENCE','ALIBABA_CLOUD_LEASE_PROVISIONER_ROLE_ARN','ALIBABA_CLOUD_LEASE_OBSERVER_ROLE_ARN','ALIBABA_CLOUD_LEASE_RELEASER_ROLE_ARN','WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY']
ENVS=['cloud-lease-provision','cloud-lease-observe','cloud-lease-release','cloud-deployment']
WORKFLOWS=['chat-lifecycle-rehearsal.yml','chat-lifecycle-rehearsal-finalize.yml','chat-lifecycle-formal.yml','chat-lifecycle-formal-finalize.yml','chat-lifecycle-stop.yml','cloud-lease-oidc-setup.yml','cloud-lease-provision.yml','cloud-lease-observe.yml','cloud-lease-release.yml','cloud-lease-analyze.yml','cloud-deployment-bundle.yml','cloud-deployment-activate.yml']
def get(path, transform=None):
    p=subprocess.run(['gh','api','--method','GET',PREFIX+path,'-H','X-GitHub-Api-Version: 2026-03-10'],text=True,capture_output=True,timeout=60)
    try: data=json.loads(p.stdout)
    except ValueError: data=None
    if p.returncode==0 and transform: data=transform(data)
    return {'endpoint':PREFIX+path,'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'exit_code':p.returncode,'data':data,'error':p.stderr.strip() or None}
def compact_runs(d):
    fields=['id','run_number','run_attempt','name','path','event','status','conclusion','head_sha','head_branch','created_at','updated_at','run_started_at','html_url']
    return {'total_count':d['total_count'],'limit':100,'workflow_runs':[{k:r.get(k) for k in fields} for r in d['workflow_runs']]}
def compact_secrets(d):
    allowed={'WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY','ALIBABA_CLOUD_ACCESS_KEY_ID','ALIBABA_CLOUD_ACCESS_KEY_SECRET'}
    return {'total_count':d['total_count'],'allowlisted_secret_metadata':[x for x in d['secrets'] if x['name'] in allowed], 'values_requested':False}
def compact_vars(d):
    return {'total_count':d['total_count'],'variables':[{k:x[k] for k in ['name','created_at','updated_at']} for x in d['variables']]}
tasks=[('oidc-subject','actions/oidc/customization/sub',None),('main-ref','git/ref/heads/main',None),('main-protection','branches/main/protection',None),('rulesets','rulesets?includes_parents=true&per_page=100',None),('repository-variable-names','actions/variables?per_page=100',compact_vars),('repository-secret-names','actions/secrets?per_page=100',compact_secrets)]
for v in VARIABLES: tasks.append(('variable-'+v,'actions/variables/'+v,None))
for e in ENVS:
    tasks += [('environment-'+e,'environments/'+e,None),('environment-variables-'+e,'environments/'+e+'/variables?per_page=100',compact_vars),('environment-secrets-'+e,'environments/'+e+'/secrets?per_page=100',compact_secrets)]
for w in WORKFLOWS:
    tasks += [('workflow-'+w,'actions/workflows/'+w,None),('runs-'+w,'actions/workflows/'+w+'/runs?per_page=100',compact_runs)]
def run(t):
    name,path,transform=t
    result=get(path,transform)
    dest=OUT/(name+'.json')
    with dest.open('x') as f: json.dump(result,f,indent=2,ensure_ascii=False);f.write('\n')
    return {'file':dest.name,'exit_code':result['exit_code']}
with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool: results=list(pool.map(run,tasks))
with (OUT/'collection-index.json').open('x') as f: json.dump({'method':'GET only','repository':REPO,'results':results},f,indent=2);f.write('\n')
print(json.dumps({'files':len(results),'errors':[r for r in results if r['exit_code']]},indent=2))
