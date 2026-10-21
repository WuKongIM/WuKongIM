from pathlib import Path
import json,hashlib,importlib.util,sys,datetime
sys.dont_write_bytecode=True
p=Path(__file__).resolve().parent;oldroot=p.parent/'rc-live-fc182/terminal-public-frozen';source=p/'source/scripts/chat-lifecycle/authenticate-released-preclock.py'
spec=importlib.util.spec_from_file_location('approved_readonly_auth',source);h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)
# Replace network seam with a hard rejection so this run cannot query authority or dispatch anything.
h.api=lambda *a,**k: (_ for _ in ()).throw(RuntimeError('offline checker forbids network'))
manifest={line.split('  ',1)[1]:line.split('  ',1)[0] for line in (oldroot/'SHA256SUMS').read_text().splitlines()}
inputs=[];docs={}
for name in h.SELECTED:
 f=oldroot/'evidence'/name;b=f.read_bytes();digest=hashlib.sha256(b).hexdigest();assert digest==manifest['evidence/'+name];docs[name]=h.strict_json(b);inputs.append({'path':str(f),'bytes':len(b),'sha256':digest})
public=json.loads((p.parent/'rc-start-fc182/rehearsal-dispatch.json').read_text())['inputs'];key=public['codex_diagnostic_pubkey']
env={'GITHUB_REPOSITORY':'WuKongIM/WuKongIM','GITHUB_REPOSITORY_ID':638054601,'WK_CHAT_REQUEST_ID':public['request_id'],'WK_CHAT_OPERATOR':'tangtaoit','WK_CHAT_SOURCE_SHA':public['source_sha'],'WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY':key}
load=lambda n:json.loads((p/'github-readonly'/f'{n}.json').read_text())
old,acquire,release=map(load,['predecessor-run','acquire-run','release-run'])
for run,path,status in [(old,h.REHEARSAL_PATH,'failure'),(acquire,'.github/workflows/cloud-lease-provision.yml','success'),(release,'.github/workflows/cloud-lease-release.yml','success')]:h.authenticate_run(run,env,path,conclusion=status)
jobs=load('release-jobs');assert jobs['total_count']==len(jobs['jobs']);steps=[s for j in jobs['jobs'] for s in j['steps'] if s['name']=='Release and retain residual evidence'];assert len(steps)==1
result={'schema':'wukongim.offline_prior_public_document_check/v1','executed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source_sha':'e30299fa59ea6669ea71024eabe209840f241f19','helper_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'inputs':inputs,'network_seam_disabled':True,'current_run_fabricated':False,'complete_continuation_authentication':False,'scope':'authenticate_run structural checks on saved fresh API objects + validate_prior_documents against locally hash-frozen historical public documents; no ZIP re-download or successor validation'}
try:
 receipt,zero,cost=h.validate_prior_documents(env,docs,old,acquire,release,steps[0]);assert cost==74477600
 result.update({'status':'passed','reserve_micros':cost,'actual_bill':None,'quote_to_zero_seconds':(h.instant(zero['observed_at'])-h.instant(docs['attempt-1/quote.json']['quote']['quoted_at'])).total_seconds(),'zero_observed_at':zero['observed_at'],'release_step':steps[0],'lease_plan_digest':receipt['plan_digest'],'complete_zero_scopes':zero['scopes']})
except h.Rejected as e:
 result.update({'status':'rejected','reason':str(e)})
(p/'offline-public-documents-check.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({k:v for k,v in result.items() if k not in ['inputs','complete_zero_scopes','release_step']}));sys.exit(0 if result['status']=='passed' else 1)
