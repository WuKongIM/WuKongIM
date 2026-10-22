#!/usr/bin/env python3
import concurrent.futures,datetime,hashlib,json,os,secrets,subprocess
from pathlib import Path
D=Path(__file__).parent;B=D.parent;R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM');P=B/'next-request-after-pr1024-merged-preparation';V=B/'next-request-after-pr1024-merged-review';PRIVATE=B/'pr1024-authorized-run-resume-20261023-private'
C='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06'; TREE='a13367b2dc4658e6c27a1992f240544636e5178d'
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def h(v):return hashlib.sha256(v).hexdigest()
def save(n,v):
 p=D/('guard/'+n);p.parent.mkdir(parents=True,exist_ok=True)
 with p.open('x') as f:json.dump(v,f,ensure_ascii=False,indent=2);f.write('\n')
def require(v,m):
 if not v:raise RuntimeError(m)
def blob(p):return subprocess.check_output(['git','show',C+':'+p],cwd=R)
def get(name,endpoint,fields=(),hash_variable=False):
 cmd=['gh','api','--hostname','github.com','--method','GET',endpoint]
 for k,v in fields:cmd+=['-f',k+'='+v]
 t=now()
 try:r=subprocess.run(cmd,cwd=R,capture_output=True,timeout=60)
 except subprocess.TimeoutExpired as e:
  save('reads/'+name+'-failure.json',{'started_at':t,'observed_at':now(),'endpoint':endpoint,'status':'timeout','stderr_sha256':h(e.stderr or b'')});raise RuntimeError('GET timeout: '+name)
 require(r.returncode==0,'GET failed (no action taken): '+name)
 value=json.loads(r.stdout);rec={'started_at':t,'observed_at':now(),'endpoint':endpoint,'exit_code':0,'response_bytes':len(r.stdout),'response_sha256':h(r.stdout)}
 if hash_variable:
  require(value.get('name')==name and isinstance(value.get('value'),str),'invalid variable')
  value={'name':name,'present':bool(value['value']),'value_sha256':h(value['value'].encode())}
 rec['data']=value;save('reads/'+name+'.json',rec);return value
if __name__=='__main__':
 request=json.loads((B/'pr1024-authorized-run-v1/authorization.json').read_text())['request_id']

 frozen=[]
 for root in [P,V]:
  manifest=root/'SHA256SUMS';entries=[]
  for line in manifest.read_text().splitlines():
   digest,path=line.split(None,1);path=path.lstrip('*');p=root/path;require(p.is_file() and h(p.read_bytes())==digest,'prior frozen manifest mismatch '+str(p));entries.append(path)
  frozen.append({'directory':str(root),'manifest_sha256':h(manifest.read_bytes()),'files_verified':len(entries)})
 plan=json.loads((P/'plan.json').read_text());require(plan['final_control_sha']==plan['final_product_source_sha']==C,'plan source mismatch')
 rows=json.loads((P/'rules-and-source-freeze.json').read_text())['files']
 for e in rows:
  b=blob(e['path']);require(len(b)==e['bytes'] and h(b)==e['sha256'],'source changed '+e['path'])
 require(subprocess.check_output(['git','rev-parse',C+'^{tree}'],cwd=R,text=True).strip()==TREE,'tree mismatch')
 save('rules-and-source-freeze.json',{'source_sha':C,'control_sha':C,'tree':TREE,'files':rows,'prior_manifests':frozen,'all_match':True})
 main=get('main-before','repos/WuKongIM/WuKongIM/git/ref/heads/main');require(main['object']['sha']==C,'remote main changed')
 old=json.loads((B/'rc-readiness-fc182-current/summary.json').read_text());expected={x['name']:x['value_sha256'] for x in old['identity_variables']};require(len(expected)==8,'expected8vars')
 def variable(name):
  v=get(name,'repos/WuKongIM/WuKongIM/actions/variables/'+name,hash_variable=True);return v|{'matches_frozen':v['value_sha256']==expected[name]}
 with concurrent.futures.ThreadPoolExecutor(max_workers=4) as ex:vars=list(ex.map(variable,expected))
 require(all(v['present'] and v['matches_frozen'] for v in vars),'public identity/wrapping mismatch')
 subject=get('oidc-subject','repos/WuKongIM/WuKongIM/actions/oidc/customization/sub');require(subject==old['oidc_subject'],'OIDC subject changed')
 names=['chat-lifecycle-rehearsal.yml','chat-lifecycle-rehearsal-finalize.yml','chat-lifecycle-formal.yml','chat-lifecycle-formal-finalize.yml','chat-lifecycle-stop.yml','cloud-deployment-bundle.yml','cloud-deployment-activate.yml','cloud-lease-provision.yml','cloud-lease-release.yml','cloud-lease-observe.yml']
 def wf(name):return name,get('workflow-'+name,'repos/WuKongIM/WuKongIM/actions/workflows/'+name)
 with concurrent.futures.ThreadPoolExecutor(max_workers=4) as ex:workflows=dict(ex.map(wf,names))
 for n in names:
  allowed=['active','disabled_manually'] if n in ['chat-lifecycle-rehearsal-finalize.yml','chat-lifecycle-formal.yml','chat-lifecycle-formal-finalize.yml','cloud-lease-release.yml'] else ['active']
  require(workflows[n]['state'] in allowed,'unexpected workflow state '+n)
 latest=get('latest-top','repos/WuKongIM/WuKongIM/actions/workflows/chat-lifecycle-rehearsal.yml/runs',[('per_page','1')])['workflow_runs'][0];require(latest['status']=='completed' and latest['run_attempt']==1,'latest paid top not terminal attempt1')
 active=[];active_bounds=[]
 for status in ['in_progress','queued','waiting','requested','pending']:
  for page in range(1,11):
   data=get('active-'+status+'-'+str(page),'repos/WuKongIM/WuKongIM/actions/runs',[('status',status),('per_page','100'),('page',str(page))]);runs=data['workflow_runs']
   active += [x for x in runs if x['path'].split('/')[-1].split('@')[0] in names]
   if len(runs)<100:active_bounds.append({'status':status,'pages':page,'complete':True});break
  else:raise RuntimeError('active runs inventory bound exceeded')
 require(not active,'active protected workflow exists')
 inventory=[];pages=0
 for page in range(1,201):
  data=get('artifacts-'+str(page),'repos/WuKongIM/WuKongIM/actions/artifacts',[('per_page','100'),('page',str(page))]);inventory+=data['artifacts'];pages=page
  if len(data['artifacts'])<100:break
 else:raise RuntimeError('200page artifact bound exceeded')
 retained=[x for x in inventory if not x['expired']];markers=[x for x in retained if x['name'].startswith('chat-lifecycle-')]
 save('retained-artifact-inventory.json',{'complete':True,'artifacts':retained})
 save('bounded-control-inventory.json',{'observed_at':now(),'artifact_pages':pages,'all_artifacts':len(inventory),'nonexpired':len(retained),'complete':True,'chat_lifecycle_markers':markers,'active_workflow_runs':active,'active_run_bounds':active_bounds,'limits':'GitHub retained Artifact metadata only; deleted/expired evidence and live provider inventory are not proved. Exact workflow repeats authoritative guards before procurement.'})
 # Invoke the unchanged exact-source read-only authority helpers. Retained candidates alone are not active authority.
 token=subprocess.check_output(['gh','auth','token','--hostname','github.com'],cwd=R,text=True).strip()
 selectors=[]
 for stage in ['rehearsal','formal']:
  env=os.environ.copy();env.update(GH_TOKEN=token,GITHUB_REPOSITORY='WuKongIM/WuKongIM',WK_CHAT_STAGE=stage)
  result=D/('active-'+stage+'-authenticated-matrix.json')
  cmd=['bash','scripts/chat-lifecycle/discover-active-handoffs.sh','',str(result)]
  if stage=='rehearsal':cmd.append(str(D/'authoritative-retained-inventory.json'))
  begun=now()
  r=subprocess.run(cmd,cwd=PRIVATE/'source',env=env,capture_output=True,timeout=600)
  save('authority-'+stage+'.json',{'started_at':begun,'observed_at':now(),'exit_code':r.returncode,'stdout_bytes':len(r.stdout),'stdout_sha256':h(r.stdout),'stderr_bytes':len(r.stderr),'stderr_sha256':h(r.stderr),'source_sha':C,'provider_calls':False})
  (PRIVATE/('authority-'+stage+'.stderr')).write_bytes(r.stderr)
  require(r.returncode==0,'exact authority helper failed '+stage)
  matrix=json.loads(result.read_text());selectors.append({'stage':stage,'authenticated_active_handoffs':len(matrix['include'])});require(not matrix['include'],'authenticated active handoff exists')
 del token
 search=get('issue-before','search/issues',[('q','repo:WuKongIM/WuKongIM is:issue in:title "[chat-lifecycle] '+request+'"'),('per_page','100')]);require(search['incomplete_results'] is False and search['total_count']==len(search['items']) and len(search['items'])<=1,'request issue ambiguous');require(not search['items'] or (search['items'][0]['title']=='[chat-lifecycle] '+request and search['items'][0]['state']=='open' and search['items'][0]['locked'] is False),'request issue not ready')
 main2=get('main-after','repos/WuKongIM/WuKongIM/git/ref/heads/main');require(main2['object']['sha']==C,'main changed during guard')
 summary={'observed_at':now(),'guard_pass':True,'request_id':request,'source_sha':C,'control_sha':C,'tree':TREE,'main_before_and_after_exact':True,'all_frozen_files_match':True,'identity_variables':vars,'oidc_subject_unchanged':True,'latest_top':{k:latest[k] for k in ['id','run_number','run_attempt','status','conclusion','head_sha','html_url']},'workflow_states':{n:{k:w[k] for k in ['id','state','path']} for n,w in workflows.items()},'bounded_inventory_complete':True,'active_protected_runs':0,'retained_handoff_selectors':selectors,'retained_chat_markers':len(markers),'provider_calls':False,'workflow_mutations':False,'dispatched':False,'limitations':['Metadata equality is not new live OIDC/wrapping validation','Live provider inventory/Quote/capacity remain exact workflow pre-Acquire gates','Point-in-time main guard is not atomic dispatch CAS','Three idle safety workflows may be disabled; their existing owner arms them before paid stages','Third live MCP attempt not authorized']}
 save('fresh-public-guard.json',summary);print(json.dumps({k:summary[k] for k in ['guard_pass','request_id','latest_top','workflow_states','bounded_inventory_complete','active_protected_runs','retained_handoff_selectors','retained_chat_markers']},indent=2))
