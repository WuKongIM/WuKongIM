#!/usr/bin/env python3
"""Bounded GET-only GitHub readiness snapshot; no cloud, secret values or payloads."""
import argparse,concurrent.futures,datetime,hashlib,json,re,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--output-dir',required=True,type=Path);p.add_argument('--previous-dir',required=True,type=Path);a=p.parse_args()
REPO='WuKongIM/WuKongIM';PREFIX='repos/'+REPO+'/';SOURCE='fc18221282f289088ec9b4282fda8e0bfc67a3b8'
VARIABLES=['ALIBABA_CLOUD_LEASE_REGION','ALIBABA_CLOUD_LEASE_ACCOUNT_ID_HASH','ALIBABA_CLOUD_LEASE_OIDC_PROVIDER_ARN','ALIBABA_CLOUD_LEASE_OIDC_AUDIENCE','ALIBABA_CLOUD_LEASE_PROVISIONER_ROLE_ARN','ALIBABA_CLOUD_LEASE_OBSERVER_ROLE_ARN','ALIBABA_CLOUD_LEASE_RELEASER_ROLE_ARN','WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY']
ENVS=['cloud-lease-provision','cloud-lease-observe','cloud-lease-release','cloud-deployment']
WORKFLOWS=['chat-lifecycle-rehearsal.yml','chat-lifecycle-rehearsal-finalize.yml','chat-lifecycle-formal.yml','chat-lifecycle-formal-finalize.yml','chat-lifecycle-stop.yml','cloud-lease-oidc-setup.yml','cloud-lease-provision.yml','cloud-lease-observe.yml','cloud-lease-release.yml','cloud-lease-analyze.yml','cloud-deployment-bundle.yml','cloud-deployment-activate.yml']
PROTECTED=['cloud-lease-provision.yml','chat-lifecycle-rehearsal.yml','chat-lifecycle-formal.yml','chat-lifecycle-rehearsal-finalize.yml','chat-lifecycle-formal-finalize.yml','chat-lifecycle-stop.yml','cloud-lease-release.yml','cloud-deployment-activate.yml','cloud-deployment-bundle.yml','cloud-lease-oidc-setup.yml','cloud-lease-observe.yml','cloud-lease-analyze.yml']
SECRETS={'WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY','ALIBABA_CLOUD_ACCESS_KEY_ID','ALIBABA_CLOUD_ACCESS_KEY_SECRET'}
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def sha(value):return hashlib.sha256(value).hexdigest()
def save(name,value):
 with (a.output_dir/name).open('x') as f:json.dump(value,f,ensure_ascii=False,indent=2);f.write('\n')
def get(path):
 start=now()
 r=subprocess.run(['gh','api','--method','GET',PREFIX+path,'-H','X-GitHub-Api-Version: 2026-03-10'],capture_output=True,text=True,timeout=60)
 try:d=json.loads(r.stdout)
 except ValueError:d=None
 return {'endpoint':PREFIX+path,'started_at':start,'observed_at':now(),'exit_code':r.returncode,'data':d,'stderr_size':len(r.stderr.encode()),'stderr_sha256':sha(r.stderr.encode())}
def require_result(record):
 if record['exit_code']!=0 or record['data'] is None:raise RuntimeError('GitHub GET failed for '+record['endpoint'])
 return record['data']
def metadata_list(endpoint,key,transform,max_pages=20):
 rows=[];pages=[];complete=False
 for page in range(1,max_pages+1):
  r=get(endpoint+('&' if '?' in endpoint else '?')+f'per_page=100&page={page}');d=require_result(r);items=d[key]
  pages.append({k:r[k] for k in ['endpoint','started_at','observed_at','exit_code']}|{'total_count':d.get('total_count'),'count':len(items),'full_page_json_sha256':sha(json.dumps(d,sort_keys=True,separators=(',',':')).encode())})
  rows.extend(transform(x) for x in items)
  if len(items)<100:complete=True;break
 return {'complete_paginated_snapshot':complete,'page_limit':max_pages,'per_page':100,'pages':pages,'rows':rows,'total_count_stable':len({v['total_count'] for v in pages})==1}
def variable(d):
 v=d['value'];name=d['name'];old=json.loads((a.previous_dir/('variable-'+name+'.json')).read_bytes())['data']['value']
 display=re.sub(r'(?<=::)[0-9]+(?=:)', '<account-id-redacted>',v) if name.endswith('_ARN') else v
 if name=='WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY':display=v.split()[0]+' <public-key-bytes-omitted>'
 return {'name':name,'present':bool(v),'redacted_value':display,'value_sha256':sha(v.encode()),'equals_0456_snapshot':v==old,'created_at':d.get('created_at'),'updated_at':d.get('updated_at')}
def envinfo(d):return {k:d.get(k) for k in ['id','name','created_at','updated_at','protection_rules','deployment_branch_policy']}
def wfinfo(d):return {k:d.get(k) for k in ['id','name','path','state','created_at','updated_at','html_url']}
tasks=[('main-before.json','git/ref/heads/main',None),('source-commit.json','git/commits/'+SOURCE,lambda d:{k:d.get(k) for k in ['sha','tree','parents']}),('oidc-subject.json','actions/oidc/customization/sub',None),('main-protection.json','branches/main/protection',None),('rulesets.json','rulesets?includes_parents=true&per_page=100',None)]
for v in VARIABLES:tasks.append(('variable-'+v+'.json','actions/variables/'+v,variable))
for e in ENVS:tasks.append(('environment-'+e+'.json','environments/'+e,envinfo))
for w in WORKFLOWS:tasks.append(('workflow-'+w+'.json','actions/workflows/'+w,wfinfo))
def run(task):
 n,url,transform=task;r=get(url)
 if r['exit_code']==0 and transform:r['data']=transform(r['data'])
 save(n,r);return {'file':n,'exit_code':r['exit_code']}
with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:results=list(pool.map(run,tasks))
save('configuration-index.json',{'method':'GET','results':results,'secret_values_requested':False,'account_id_redacted':True})
for label,base in [('repository','actions'),*[(e,'environments/'+e) for e in ENVS]]:
 for key in ['variables','secrets']:
  def transform(x):return {k:x.get(k) for k in ['name','created_at','updated_at']}
  r=metadata_list(base+'/'+key,key,transform)
  if key=='secrets':
   r['non_allowlisted_metadata_count']=sum(x['name'] not in SECRETS for x in r['rows'])
   r['rows']=[x for x in r['rows'] if x['name'] in SECRETS];r['allowlist']=sorted(SECRETS)
  r['secret_values_requested' if key=='secrets' else 'values_retained']=False;save(label+'-'+key+'-metadata.json',r)
fields=['id','run_attempt','name','path','event','status','conclusion','head_sha','head_branch','created_at','updated_at','html_url']
def compact_run(d):return {k:d.get(k) for k in fields}|{'repository':d['repository']['full_name'],'head_repository':(d.get('head_repository') or {}).get('full_name')}
active=[]
for status in ['queued','in_progress','waiting','requested','pending']:
 r=metadata_list('actions/runs?status='+status,'workflow_runs',compact_run);r['status_filter']=status;active.append(r)
save('nonterminal-runs.json',{'observed_at':now(),'protected_workflows':PROTECTED,'filters':active})
artifacts=metadata_list('actions/artifacts','artifacts',lambda x:{k:x.get(k) for k in ['id','name','size_in_bytes','created_at','updated_at','expires_at','expired','digest','workflow_run']},max_pages=200)
all_count=len(artifacts['rows']);ids=[x['id'] for x in artifacts['rows']]
artifacts['total_rows_observed']=all_count;artifacts['unique_ids']=len(set(ids));artifacts['inventory_count_matches_advertised']=bool(artifacts['pages']) and len(set(ids))==artifacts['pages'][-1]['total_count']
artifacts['rows']=[x for x in artifacts['rows'] if x['name'].startswith(('chat-lifecycle-','cloud-lease-'))]
artifacts['selection_prefixes']=['chat-lifecycle-','cloud-lease-'];artifacts['payload_downloaded']=False
artifacts['limit']='Retained GitHub metadata only. Deleted/expired evidence and provider resources are not proved absent; handoff may contain cleanup-pending internally.'
save('retained-artifact-metadata.json',artifacts)
save('main-after.json',get('git/ref/heads/main'))
print(json.dumps({'configuration_requests':len(results),'configuration_failures':[x for x in results if x['exit_code']],'active_runs':{x['status_filter']:len(x['rows']) for x in active},'artifact_pages':len(artifacts['pages']),'artifact_total':all_count,'selected_artifacts':len(artifacts['rows']),'metadata_complete':artifacts['complete_paginated_snapshot'],'counts_match':artifacts['inventory_count_matches_advertised']}))
