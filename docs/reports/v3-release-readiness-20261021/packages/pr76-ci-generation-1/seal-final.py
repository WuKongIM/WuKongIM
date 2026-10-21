#!/usr/bin/env python3
"""Bounded local evidence sealing; no execution of candidate code or network writes."""
import datetime,hashlib,json,pathlib,re,subprocess
P=pathlib.Path('/tmp/wukongim-v3-readiness-20261021/packages/pr76-ci-generation-1')
OLD=P.parent/'pr75-ci-generation-3'
W=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-object-provider')
HEAD='dded26afa12188d5f1c036af4b9c06c3cbddb46d'
OLDHEAD='2368c8745853d7d8ad82e706a6e7980842526241'
def sha(b): return hashlib.sha256(b).hexdigest()
def read(p): return json.loads(p.read_bytes())
def write(name,data): (P/name).write_text(json.dumps(data,indent=2,ensure_ascii=False)+'\n')
def git(*args): return subprocess.run(['git','-C',str(W),*args],check=True,capture_output=True,timeout=15).stdout
def fact(p): return {'path':str(p),'bytes':p.stat().st_size,'sha256':sha(p.read_bytes())}
now=datetime.datetime.now(datetime.timezone.utc).isoformat()
receiptpath='artifacts/native-stable-four-clients-test-only/native-stable-clients.json'
a=read(P/receiptpath);b=read(OLD/receiptpath)
assert git('rev-parse','HEAD').decode().strip()==HEAD
assert git('status','--porcelain=v1')==b''
assert git('rev-parse',HEAD+'^{tree}').decode().strip()==a['control']['tree']
assert git('rev-parse',OLDHEAD+'^{tree}').decode().strip()==b['control']['tree']
failures=[(i,c) for i,c in enumerate(a['commands']) if c['exit_code'] != 0]
assert len(failures)==1
idx,failed=failures[0]; create=a['commands'][idx-1]
assert idx==66 and create['output_tail'].strip()==failed['command'][-1]
assert create['command'][-6:-3]==['600s','bash','-euc'] or '600s' in create['command']
first={
 'recorded_at':now,'run_id':39644526993,'run_attempt':1,'job_id':119376463184,
 'receipt':fact(P/receiptpath),'phase':a['phase'],'failure':a['failure'],'exercise_failure':a['exercise_failure'],
 'case_count':{'expected':49,'recorded':len(a['cases']),'passed':sum(c['passed'] for c in a['cases']),'failed':sum(not c['passed'] for c in a['cases']),'not_reached':49-len(a['cases'])},
 'first_failed_command_index':idx,'first_failed_command':failed,'container_creation':create,
 'assertion':{'path':'tests/native_stable_clients_drill.py','line':850,'text':"raise RuntimeError('real installed client failed' if installed else 'real download client failed')"},
 'wrapped_assertion':{'path':'scripts/validate-production-package-clients.py','line':1599,'text':'raise ClientValidationError("stable client verification failed: " + str(error)) from error'},
 'timeout':{'container_seconds':600,'container_kill_after_seconds':10,'driver_seconds':650,'outer_workflow_minutes':55,'receipt_timed_out':failed['timed_out'],'explanation':'Container command contains timeout 600s and exits 124 after 600.218s. Driver did not hit its separate 650s timeout.'},
 'boundary':{'package_hash_check_observed':'/candidate.rpm: OK' in failed['output_tail'],'package_signature_check_observed':'digests signatures OK' in failed['output_tail'],'last_observed_output':'AlmaLinux 9 AppStream metadata progress: 30 MB, 05:59','dependency_resolution_completed_observed':False,'package_transaction_observed':False,'installed_cli_acceptance_observed':False,'causal_limit':'No internal timing, selected Alma mirror URL, network trace, or resource metrics. Exit 124 is consistent with the explicit inner deadline; exact DNF wait and external root cause remain unknown.'},
 'cleanup':a['cleanup'],'reader_cleanup':a['reader_cleanup'],'cache_cleanup':a['cache_cleanup'],
 'earlier_installed_subcommands':[{'index':i,**{k:c[k] for k in ('stage','exit_code','elapsed_seconds','timed_out','output_sha256')}} for i,c in enumerate(a['commands']) if c['stage']=='installed-client' and i<idx],
 'request_barrier_cases_reached':False,'cache_cases_reached':False,
}
write('first-failure.json',first)
# Bind unchanged pre-existing executable/control bytes, plus explicit eight-file diff.
paths=git('ls-tree','-r','--name-only',OLDHEAD).decode().splitlines()
relevant=[p for p in paths if p.startswith(('scripts/','tests/','manifests/','keys/','toolchain/')) or p=='.github/workflows/native-package-repository-drill.yml']
source=[]
for path in relevant:
 old=git('show',OLDHEAD+':'+path);new=git('show',HEAD+':'+path)
 assert old==new,path
 source.append({'path':path,'sha256':sha(new),'bytes':len(new),'git_blob':git('rev-parse',HEAD+':'+path).decode().strip()})
changed=git('diff','--name-status',OLDHEAD,HEAD).decode().splitlines();assert len(changed)==8
binaries=[]
for key,value in a['product_build']['binaries'].items():
 old=b['product_build']['binaries'][key]
 assert value['sha256']==old['sha256'] and value['size']==old['size']
 assert value['go_version_m'].splitlines()[1:]==old['go_version_m'].splitlines()[1:]
 binaries.append({'name':key,'sha256':value['sha256'],'size':value['size'],'go_version_m_only_difference':'first output line temporary binary path'})
assert a['images']==b['images'] and a['host_tools']==b['host_tools']
comparison={
 'recorded_at':now,'prior_receipt':fact(OLD/receiptpath),'current_receipt':fact(P/receiptpath),
 'prior_head':OLDHEAD,'prior_actual_checkout':b['control'],'current_head':HEAD,'current_actual_checkout':a['control'],
 'git_tree_matches_receipt_both':True,'changed_files':changed,'unchanged_existing_control_files':source,
 'images_equal':True,'images':a['images'],'host_tools_equal':True,
 'product_build':{k:v for k,v in a['product_build'].items() if k!='binaries'},'binaries_equal':binaries,
 'prior_cases':{'passed':sum(c['passed'] for c in b['cases']),'total':len(b['cases'])},
 'baseline_installed_comparison':[{'distribution':next(s for s in a['commands'][i-1]['command'] if '@sha256:' in s),'current':{'index':i,**{k:a['commands'][i][k] for k in ('exit_code','elapsed_seconds','output_size','output_sha256')}},'prior':{'index':i,**{k:b['commands'][i][k] for k in ('exit_code','elapsed_seconds','output_size','output_sha256')}}} for i in (52,56,62,66)],
 'prior_alma_baseline_output':b['commands'][66]['output_tail'],
 'current_rocky_baseline_output':a['commands'][62]['output_tail'],
 'scope_limit':'No changed executable on observed native failing path. D2 SDK workflow is a separate job and native job does not install its lock or call the new provider/input libraries. These facts do not prove complete causal independence; external repositories, host state and network path were not frozen.',
 'causal_conclusion':'Unknown. Supporting evidence for distribution-repository delay, not unique external-network attribution.',
}
write('pr75-comparison.json',comparison)
# Keep the first bounded checkpoint immutable.
stage=read(P/'stage-freeze-1.json')
for f in stage['files']:
 p=P/f['path'];assert p.stat().st_size==f['bytes'] and sha(p.read_bytes())==f['sha256'],str(p)
write('stage-freeze-verification.json',{'verified_at':now,'original_manifest':fact(P/'stage-freeze-1.json'),'files':len(stage['files']),'all_unchanged':True})
pr=read(P/'pr-final.json');jobs=read(P/'native-jobs-final.json')['jobs'];run=read(P/'native-run-final.json')
assert pr['head']['sha']==HEAD and pr['draft'] is True and pr['merged'] is False
assert run['status']=='completed' and run['conclusion']=='failure' and run['run_attempt']==1
assert all(j['status']=='completed' for j in jobs)
sdk=read(P/'artifacts/object-storage-sdk-contract/provider-contract-receipt.json')
assert sdk['tests']==22 and sdk['skips']==0 and sdk['failures']==0 and sdk['errors']==0
candidate=read(P/'artifacts/native-stable-candidate-test-only/native-stable-candidate.json')
summary={
 'recorded_at':now,'pr_url':pr['html_url'],'head':HEAD,'base':pr['base']['sha'],'actual_checkout':a['control'],'draft':True,'merged':False,
 'all_runs_terminal':True,'overall_passed':False,'run_attempt':1,
 'pages':{'run':39644526518,'conclusion':'success','default_tests':567,'default_skips':0,'contract_tests':4,'deploy_job':'skipped: pull_request'},
 'sdk':{'run':39644526769,'conclusion':'success','tests':22,'skips':0,'inputs_tests':10,'inputs_skips':0,'receipt':fact(P/'artifacts/object-storage-sdk-contract/provider-contract-receipt.json'),'locked_install_succeeded':True},
 'native':{'run':39644526993,'conclusion':'failure','jobs':[{k:j[k] for k in ('id','name','status','conclusion','html_url')} for j in jobs],'intermediate_receipts':read(P/'native-intermediate-receipts.json'),'candidate_transport_tests':len(candidate['transport_cases']),'stable':first['case_count'],'cleanup_verified':a['cleanup']['verified'],'reader_cleanup_verified':a['reader_cleanup']['verified']},
 'original_standards_process_P2_remains':'Two original HTTP wiring unit tests were added after implementation. No claim that all implementation was tests-first.',
 'limits':['No passing full 49-case PR76 result.','No live provider, production activation, publication, or full new D2 production-shaped native closure established.','Cause of Alma DNF deadline remains unknown.','Local green results were not substituted for Linux CI.'],
 'actions':'GitHub GET and local evidence writes only. No rerun/cancel/dispatch/push/merge/source edits/local tests. All local collection commands reached terminal state.',
}
write('summary.json',summary)
write('source-binding.json',{'recorded_at':now,'worktree':str(W),'head':HEAD,'tree':a['control']['tree'],'parent':git('rev-parse','HEAD^').decode().strip(),'worktree_clean':True,'checkout_proof':fact(P/'checkout-source-binding.json'),'first_failure':fact(P/'first-failure.json'),'comparison':fact(P/'pr75-comparison.json'),'rules':[fact(pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/AGENTS.md')),fact(pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.github/workflows/README.md')),fact(W/'docs/release-contract.md'),fact(W/'docs/stable-audit-transport.md')],'packages_tracked_AGENTS_FLOW':git('ls-files','*AGENTS.md','*FLOW.md').decode().splitlines()})
print(json.dumps({'terminal':True,'overall_passed':False,'unchanged_control_files':len(source),'case_count':first['case_count'],'current_binary_sha256_all_equal_prior':True,'stage_checkpoint_unchanged':True}))
