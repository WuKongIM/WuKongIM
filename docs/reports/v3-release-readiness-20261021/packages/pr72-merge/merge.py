from pathlib import Path
import subprocess,json,datetime
p=Path(__file__).parent
repo='WuKongIM/packages';sha='c8aae37d13787a0aceceac751d6d35bbc1d89c35';base='0cd625cbee214927a81b0a1015d825a6f53fbf28'
def get(name,args):
 r=subprocess.run(['gh',*args],capture_output=True)
 (p/(name+'.stdout')).write_bytes(r.stdout);(p/(name+'.stderr')).write_bytes(r.stderr)
 (p/(name+'.receipt.json')).write_text(json.dumps({'command':['gh',*args],'exit_code':r.returncode,'at':datetime.datetime.now(datetime.timezone.utc).isoformat()},indent=2)+'\n')
 assert r.returncode==0,name
 v=json.loads(r.stdout);(p/(name+'.json')).write_text(json.dumps(v,indent=2)+'\n');return v
pr=get('pr-before',['pr','view','72','--repo',repo,'--json','number,title,state,isDraft,headRefOid,baseRefOid,headRefName,baseRefName,mergeStateStatus,mergeable,statusCheckRollup,reviewDecision,reviews,url'])
assert pr['headRefOid']==sha and pr['baseRefOid']==base
assert pr['state']=='OPEN' and not pr['isDraft'] and pr['mergeable']=='MERGEABLE' and pr['mergeStateStatus']=='CLEAN'
checks={x['name']:x for x in pr['statusCheckRollup']}
for name,run in [('Validate complete Pages snapshot','39579343465'),('TEST ONLY signed APT/RPM repository','39579343148')]:
 c=checks[name];assert c['status']=='COMPLETED' and c['conclusion']=='SUCCESS' and '/runs/'+run+'/' in c['detailsUrl']
assert checks['Deploy complete Pages snapshot']['conclusion']=='SKIPPED'
assert len(checks)==3
meta=get('repository',['api','repos/'+repo]);assert meta['allow_rebase_merge']
protection=get('protection',['api','repos/'+repo+'/branches/main/protection']);assert protection['required_linear_history']['enabled'] and protection['enforce_admins']['enabled']
assert not pr['reviews']
(p/'authorization.json').write_text(json.dumps({'human_response':'同意合并 #72','scope':'normal merge of exact reviewed c8aae37; no stable activation, release or cloud changes','source_sha':sha,'method':'rebase','reason':'required linear history enabled'},ensure_ascii=False,indent=2)+'\n')
body={'sha':sha,'merge_method':'rebase'};(p/'merge-input.json').write_text(json.dumps(body)+'\n')
(p/'guard.json').write_text(json.dumps({'passed':True,'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'expected_head':sha,'expected_base':base,'checks':'two required task CIs succeeded; deploy skipped','admin_bypass':False},indent=2)+'\n')
result=get('merge-result',['api','--method','PUT','repos/'+repo+'/pulls/72/merge','--input',str(p/'merge-input.json')]);assert result['merged'] is True
print(json.dumps(result))
