from pathlib import Path
import json,re,hashlib,subprocess
B=Path(__file__).parent;P=B.with_name(B.name+'-private');W=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM');H='4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac'
sha=lambda b:hashlib.sha256(b).hexdigest()
def lines(raw):
 return [re.sub(r'\x1b\[[0-9;]*m','',re.sub(r'^\d{4}-\S+Z ','',s)) for s in raw.decode().splitlines()]
items=[]
for f in sorted((B/'log-bindings').glob('job-*.json')):
 j=json.loads(f.read_text());raw=(P/'logs'/('job-'+str(j['id'])+'.log')).read_bytes();assert sha(raw)==j['sha256'];ls=lines(raw)
 v={'job_id':j['id'],'name':j['name'],'log_bytes':len(raw),'log_sha256':sha(raw),'source_checkout':j['actual_checkout_candidates'],'conclusion':j['conclusion'],'package_pass_summaries':[s for s in ls if re.fullmatch(r'ok\s+github\.com/WuKongIM/WuKongIM/[\w/]+\s+[0-9.]+s(?:\s+.*)?',s)],'successful_steps':j['successful_steps'],'skipped_steps':j['skipped_steps']}
 if j['name'].startswith('Exercise '):
  assert 'Exercise the operator-owned lifecycle' in j['successful_steps'] and 'Verify the downloaded package set' in j['successful_steps']
  assert any("-run '^TestNativePackageLifecycle$'" in s for s in ls) and any('WK_NATIVE_PACKAGE_LIFECYCLE_INTEGRATION: 1' in s for s in ls)
  v['enabled_real_systemd_lifecycle_test']=True;v['native_package_digest_check']=True;v['inner_success_output_not_retained_by_CombinedOutput_test']=True
 if j['name']=='Build and install deb/rpm preview':
  v['cosign_tool_verification_skipped']=any('cosign not found in PATH, skipping signature verification' in s for s in ls)
  v['production_native_publication']=False
 if j['name']=='PR three-node correctness':
  prefix='[chat-lifecycle-shakeout] terminal report summary: ';rows=[json.loads(s.split(prefix,1)[1]) for s in ls if prefix in s];assert len(rows)==1
  v['terminal_summary']={k:rows[0][k] for k in ['outcome','cause','hot_sendack_p99_limit_ms','hot_sendack_count','hot_sendack_mean_ms','sent','acknowledged']}
  v['full_correctness_artifact_downloaded']=False
 items.append(v)
source={}
for name in ['scripts/native_package_lifecycle_integration_test.go','scripts/validate-native-package-lifecycle-container.sh']:
 b=subprocess.check_output(['git','show',H+':'+name],cwd=W);source[name]={'bytes':len(b),'sha256':sha(b)}
out={'jobs':items,'native_lifecycle_contract_source':source,'limits':['Successful Go lifecycle wrapper consumes and suppresses inner success output; public proof binds enabled real-systemd test, exact checkout, successful step and nonverbose Go result. No independent container zero-inventory receipt is invented.','Large native/correctness artifact bodies were not downloaded; API declared digests are separate from local proof ZIP digests.','TEST ONLY signed repository fixture and unsigned preview are not stable package publication.']}
target=B/'safe-job-log-summary.json';target.write_text(json.dumps(out,indent=2)+'\n');print(json.dumps({'jobs':len(items),'native_lifecycle':sum(x.get('enabled_real_systemd_lifecycle_test',False) for x in items),'source_hashes':source}))
