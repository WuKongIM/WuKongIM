#!/usr/bin/env python3
"""Freeze only the public preparation receipts; never include the sibling private directory."""
import datetime,hashlib,json,re
from pathlib import Path
D=Path(__file__).parent
A=json.loads((D/'authorization.json').read_text());G=json.loads((D/'fresh-public-guard.json').read_text());I=json.loads((D/'issue-verification.json').read_text());P=json.loads((D/'planned-dispatch.json').read_text());K=json.loads((D/'local-diagnostic-identity.json').read_text())
assert P['executed'] is False and I['exact_search_count']==1 and I['main_exact']
summary={'schema':'wukongim.cloud_rc.preparation_summary/v1','sealed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'request_id':A['request_id'],'source_sha':A['source_sha'],'control_sha':A['control_sha'],'tree':G['tree'],'tracking_issue':I['url'],'tracking_issue_create_attempts':1,'issue_open_unlocked_unique':True,'planned_inputs':'planned-workflow-inputs.json','input_count':6,'request_budget_limit_micros':'1351044800','operational_stop_micros':1201044800,'prior_quote_reserve_micros':148955200,'reserve_is_invoice':False,'aggregate_hard_limit_micros':1500000000,'aggregate_operational_stop_micros':1350000000,'rules_and_source_files_verified':31,'seven_identity_plus_wrapping_unchanged':True,'oidc_subject_unchanged':True,'diagnostic_public_fingerprint':K['fingerprint'],'diagnostic_public_private_match':True,'private_key_in_public_archive':False,'latest_preparation_top_run':G['latest_top'],'workflow_states':G['workflow_states'],'dispatch_performed_by_this_subtask':False,'provider_calls_by_this_subtask':False,'workflow_permission_config_changes_by_this_subtask':False,'all_subtask_exec_terminal':True,'preserved_observations':['Initial formal-finalizer GET transport EOF; raw stderr is restricted 0600 outside public allowlist, public digest retained. One read-only retry succeeded.','Initial local guard incorrectly required generic release schedule already active; exact source says idle disabled is valid and paid arm_release is the enabling owner. Original scripts and correction receipt retained. No workflow change was made.','Initial complete Search immediately after successful Issue creation returned zero due to not-yet-visible search result. A subsequent read-only GET plus complete Search returned one exact matching Issue. No second create call.'],'limits':['Authorization was relayed by root from the explicit human response, preserved verbatim.','Latest top #33 failure is terminal; its failure is not relabeled success.','Old two Lease zero inventory is parent-provided prior evidence, not a new provider inventory read here.','Hash equality is public metadata continuity, not live IAM/secret/wrapping-key proof.','Identity/inventory/Quote/capacity/budget live admission belongs to the existing workflow.','No local execution of the GH_TOKEN-dependent shell preflight; exact official GET/Search fields independently checked.','Fresh main checks are point-in-time; root owns the final pre-dispatch guard and single initial dispatch.','All no-dispatch statements are this preparation snapshot, not a claim that root can never subsequently dispatch.']}
(D/'summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
readme=f'''# New authorized cloud RC request preparation

Request `{A['request_id']}`; [tracking Issue #1023]({I['url']}). Product and control are both `{A['source_sha']}`, tree `{G['tree']}`. All 31 frozen rule/source hashes match. The seven identity variables, wrapping public-key digest and exact OIDC subject match the preceding public baseline.

The user explicitly authorized the new paid request. The root task is the sole initial dispatcher. This subtask performed read-only GitHub checks, one local diagnostic Ed25519 key generation and **one Issue creation**; no workflow dispatch, provider calls, OIDC apply, workflow activation, permission or configuration change. All local commands are terminal.

`planned-workflow-inputs.json` contains exactly six workflow inputs. The request H is the string `1351044800`; derived stop S is `1201044800`. The `148955200` prior Quote reserve remains outside this request and is not a bill. Aggregate limits remain ¥1,500 / ¥1,350. `released_preclock_run_id` is empty. The planned sequence is 2h rehearsal → verified release → new Lease 72h soak → ≤8h capacity → 30m recovery; 96h is only the formal Lease deadline.

`issue-verification.json` proves fresh exact GET and complete Search identify the single open, unlocked Issue with the exact body and required repository/comments fields. Initial creation succeeded with exit 0; the immediate search result was not yet visible, so a second read-only check was used. No create retry occurred. Final `main-post-issue-create.json` still has exact cee5 source.

Latest prior paid top is #33 / 39605452697, attempt 1, completed with failure. The paid entrypoint and its direct tools are active. Four safety/continuation workflows are idle-disabled, permitted by the frozen contract: their existing stage owners arm them before their corresponding paid stage. The initial local guard's contrary generic-release requirement is preserved as a preparation-script error and corrected in `guard-contract-correction.json`; no workflow was manually enabled. A transient GET EOF and its bounded read retry are separately retained.

Private key bytes were never archived. The key is owned by this new request under the authorized local identity directory (0700; private/public files 0600); `local-diagnostic-identity.json` records only location, public key/fingerprint and successful public/private comparison. Restricted raw GitHub error stderr is outside this directory, in the 0700 sibling `rc-new-cee5-private`, excluded from every public allowlist.

Limits: public hashes do not establish live IAM or private wrapping usability. Previous cloud-zero proof is inherited prior evidence; no current provider inspection was performed. The real workflow must pass its live Quote/inventory/capacity/budget gates. Root must recheck main immediately before its authorized single dispatch. Scripts in this archive preserve executed preparation steps and failures; **do not rerun them** to create another key or Issue.

The payload allowlist is `ARCHIVE_ALLOWLIST.txt`; `manifest.json` binds each payload's size and SHA-256; `SHA256SUMS` also covers the allowlist and manifest. No private sibling files are included.
'''
(D/'README.md').write_text(readme)
meta={'manifest.json','ARCHIVE_ALLOWLIST.txt','SHA256SUMS'}
rows=[]
for p in sorted(D.rglob('*')):
 if not p.is_file() or p.name in meta:continue
 assert not p.is_symlink()
 raw=p.read_bytes()
 assert not re.search(rb'-----BEGIN [^-\n]*PRIVATE KEY-----',raw)
 assert not re.search(rb'https?://[^\s]{1,1024}[?&]X-(?:Amz|Goog)-Signature=', raw)
 rows.append({'path':str(p.relative_to(D)),'bytes':len(raw),'sha256':hashlib.sha256(raw).hexdigest()})
(D/'ARCHIVE_ALLOWLIST.txt').write_text(''.join(x['path']+'\n' for x in rows))
manifest={'scope':'public-only local preparation; private sibling excluded','payload_files':len(rows),'payload_bytes':sum(x['bytes'] for x in rows),'files':rows}
(D/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
sums=[(x['sha256'],x['path']) for x in rows]
for name in ['ARCHIVE_ALLOWLIST.txt','manifest.json']:sums.append((hashlib.sha256((D/name).read_bytes()).hexdigest(),name))
(D/'SHA256SUMS').write_text(''.join(h+'  '+n+'\n' for h,n in sums))
print(json.dumps({'payload_files':len(rows),'payload_bytes':manifest['payload_bytes'],'manifest_sha256':hashlib.sha256((D/'manifest.json').read_bytes()).hexdigest(),'sha256sums_sha256':hashlib.sha256((D/'SHA256SUMS').read_bytes()).hexdigest(),'directory':str(D)}))
