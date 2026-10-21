#!/usr/bin/env python3
"""One local RC identity, GET-only public preflight, no workflow/provider writes."""
import concurrent.futures,datetime,hashlib,json,os,re,secrets,stat,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parent
BASE=ROOT.parent
REPO=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
SOURCE='fc18221282f289088ec9b4282fda8e0bfc67a3b8'
def now():return datetime.datetime.now(datetime.timezone.utc).isoformat()
def save(n,v):
 with (ROOT/n).open('x') as f:json.dump(v,f,ensure_ascii=False,indent=2);f.write('\n')
def sha(b):return hashlib.sha256(b).hexdigest()
def get(endpoint,fields=()):
 t=now();cmd=['gh','api','--method','GET',endpoint,'-H','X-GitHub-Api-Version: 2026-03-10']
 for k,v in fields:cmd.extend(['-f',k+'='+v])
 r=subprocess.run(cmd,capture_output=True,timeout=60)
 if r.returncode:raise RuntimeError('GET failed '+endpoint+' stderr_sha256='+sha(r.stderr))
 return json.loads(r.stdout),{'endpoint':endpoint,'started_at':t,'observed_at':now(),'exit_code':r.returncode}
request='chat-'+datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')+'-'+secrets.token_hex(4)
assert re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,95}',request)
save('authorization.json',{'schema':'wukongim.rc_paid_authorization_record/v1','recorded_at':now(),'evidence_kind':'human_authorization_attested_by_parent_agent','attesting_agent':'/root','verbatim_human_message_available_here':False,'human_question_and_answer_location':'current parent conversation','authorization_relay':'用户已明确授权按fc182在杭州4台4核8G临时主机执行2h预演→释放→新租约72h长稳/≤8h容量/30m恢复，整个request累计CNY1500上限/CNY1350stop；包含临时公网SSH/HTTP、跟踪Issue、清理调度和最终释放。身份/库存/Quote不通过即停止，无自动改IAM/追加预算授权。','source_sha':SOURCE,'request_id':request,'paid_budget_cny':1500,'operational_stop_cny':1350,'iam_dns_cdn_secrets_roles_changes_authorized':False,'dispatch_owner':'/root','this_preparation_authority':'local request/key and GET-only checks; no dispatch/provider'})
snap=json.loads((BASE/'rc-readiness-fc182-current/summary.json').read_bytes())
expected={r['name']:r['value_sha256'] for r in snap['identity_variables']}
main,mainrec=get('repos/WuKongIM/WuKongIM/git/ref/heads/main');assert main['object']['sha']==SOURCE
rows=[]
def variable(name):
 d,r=get('repos/WuKongIM/WuKongIM/actions/variables/'+name);v=d['value'];h=sha(v.encode());return r|{'name':name,'present':bool(v),'value_sha256':h,'matches_frozen':h==expected[name]}
with concurrent.futures.ThreadPoolExecutor(max_workers=4) as ex:rows=list(ex.map(variable,expected))
assert all(r['present'] and r['matches_frozen'] for r in rows)
sub,subrec=get('repos/WuKongIM/WuKongIM/actions/oidc/customization/sub');assert sub==snap['oidc_subject']
main2,mainrec2=get('repos/WuKongIM/WuKongIM/git/ref/heads/main');assert main2['object']['sha']==SOURCE
query='repo:WuKongIM/WuKongIM is:issue in:title "[chat-lifecycle] '+request+'"'
issues,issrec=get('search/issues',[('q',query),('per_page','100')]);assert issues['total_count']==0 and not issues.get('incomplete_results',False)
art=json.loads((BASE/'rc-readiness-fc182-current/retained-artifact-metadata.json').read_bytes());assert all(request not in x['name'] for x in art['rows'])
save('fresh-public-guard.json',{'source_sha':SOURCE,'main_before':mainrec|{'sha':SOURCE},'main_after':mainrec2|{'sha':SOURCE},'variables':rows,'configured_count':7,'wrapping_public_present':True,'subject':subrec|{'data':sub},'guard_pass':True,'scope':'GET public values hashed only; no secret values/provider','runtime_race_limit':'Inputs are resolved at workflow runtime; this snapshot is not a server-side verify-only guarantee. Stop if any field/control changes.'})
save('request-uniqueness.json',{'request_id':request,'issue_query':query,'issue_result':issrec|{'total_count':0,'incomplete_results':False},'artifact_inventory_sha256':sha((BASE/'rc-readiness-fc182-current/retained-artifact-metadata.json').read_bytes()),'artifact_name_collision':False,'no_new_request_created_in_github':True})
private_parent=Path('/Users/tt/.wukongim-cloud-rc-identities')
if not private_parent.exists():private_parent.mkdir(mode=0o700)
assert not private_parent.is_symlink() and stat.S_IMODE(private_parent.stat().st_mode)==0o700 and private_parent.stat().st_uid==os.getuid()
private_dir=private_parent/request;private_dir.mkdir(mode=0o700)
key=private_dir/'diagnostic_ed25519'
r=subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-C',request,'-f',str(key)],capture_output=True,timeout=30);assert r.returncode==0
os.chmod(key,0o600);os.chmod(str(key)+'.pub',0o600)
pub=Path(str(key)+'.pub').read_text().strip();assert pub.startswith('ssh-ed25519 ')
derived=subprocess.run(['ssh-keygen','-y','-f',str(key)],capture_output=True,text=True,timeout=30);assert derived.returncode==0 and derived.stdout.split()[:2]==pub.split()[:2]
fingerprint=subprocess.check_output(['ssh-keygen','-lf',str(key)+'.pub'],text=True).strip()
(ROOT/'diagnostic.pub').write_text(pub+'\n')
save('local-identity.json',{'request_id':request,'private_directory':str(private_dir),'private_key_path':str(key),'public_key_path':str(ROOT/'diagnostic.pub'),'public_key':pub,'fingerprint':fingerprint,'directory_mode':oct(stat.S_IMODE(private_dir.stat().st_mode)),'private_mode':oct(stat.S_IMODE(key.stat().st_mode)),'public_private_match':True,'private_bytes_not_read_by_python_or_archived':True,'private_bytes_never_printed':True,'private_identity_generated_once':True,'generated_at':now()})
save('rehearsal-dispatch.json',{'ref':'main','inputs':{'source_sha':SOURCE,'operator':'tangtaoit','codex_diagnostic_pubkey':pub,'request_id':request}})
save('oidc-existing-verification-dispatch.json',{'ref':'main','inputs':{'setup_id':'verify-'+request,'operation':'apply','force_reconcile':False}})
assert len('verify-'+request)<=64
old=json.loads((BASE/'readiness-audit-summary.json').read_bytes())
selectors=old['historical_rehearsal_cleanup_shape_checks']
save('historical-selector-boundary.json',{'previous_summary_sha256':sha((BASE/'readiness-audit-summary.json').read_bytes()),'historical_structural_results':selectors,'all_27_pass':len(selectors)==27 and all(x['structural_check_pass'] for x in selectors),'new_artifact_payload_downloaded':False,'current_provider_zero_proved':False,'retained_handoff_count':0,'runtime_requirement':'Protected startup reauthenticates full retained handoff inventory; provider inventory requires authorized live Observer and exact selectors. No absence is inferred from historical cleanup shapes.'})
paths=['AGENTS.md','.github/workflows/README.md','.github/workflows/cloud-lease-oidc-setup.yml','.github/workflows/chat-lifecycle-rehearsal.yml','cmd/wkcloudleaseoidc/FLOW.md','cmd/wkcloudleaseoidc/main.go','internal/infra/cloudlease/alibaba/FLOW.md','internal/infra/cloudlease/alibaba/openapi.go','scripts/chat-lifecycle/discover-active-handoffs.sh','scripts/chat-lifecycle/stage-orchestrate.sh']
files=[]
for p in paths:
 b=subprocess.check_output(['git','show',SOURCE+':'+p],cwd=REPO);files.append({'path':p,'sha256':sha(b)})
save('source-manifest.json',{'source_sha':SOURCE,'control_observed_sha':SOURCE,'files':files})
print(json.dumps({'request_id':request,'public_key':pub,'private_key_path':str(key),'fresh_public_guard':True,'dispatch_file':str(ROOT/'rehearsal-dispatch.json'),'oidc_verification_file':str(ROOT/'oidc-existing-verification-dispatch.json'),'provider_called':False,'dispatched':False}))
