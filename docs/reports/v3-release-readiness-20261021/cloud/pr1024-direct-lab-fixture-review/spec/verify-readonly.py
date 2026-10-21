#!/usr/bin/env python3
"""Reconcile already captured fixture evidence and source; never run tests."""
from pathlib import Path
import copy, hashlib, json, subprocess
from datetime import datetime, timezone
R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-evidence')
D=Path('/tmp/wukongim-v3-readiness-20261021/cloud/pr1024-direct-lab-fixture-optimization-v1')
O=Path(__file__).resolve().parent
HEAD='44de9452d94edae16129b5781d794f8c1c6c7c57'
TREE='b69a44af04f20143b690e7a3860cc45caa95a0f4'
NAME='TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire'
def digest(raw): return hashlib.sha256(raw).hexdigest()
def readj(path): return json.loads(path.read_text())
def git(*args): return subprocess.check_output(['git','-C',str(R),*args])
def timestamp(): return datetime.now(timezone.utc).isoformat()
freeze_path=D/'review-source-freeze.json'
assert digest(freeze_path.read_bytes())=='af21909193565744bd478512d428494eb3afee127bc3a5bcf2146766404d9a0a'
frozen=readj(freeze_path)
entries=frozen['files']+frozen['unchanged_rules']

def snapshot():
    hashes=[]
    for item in entries:
        raw=(R/item['path']).read_bytes()
        assert digest(raw)==item['sha256'],item['path']
        hashes.append({'path':item['path'],'bytes':len(raw),'sha256':digest(raw)})
    assert git('rev-parse','HEAD').decode().strip()==HEAD
    assert git('rev-parse','HEAD^{tree}').decode().strip()==TREE
    assert not git('diff','--cached','--name-only')
    assert not git('rev-list',HEAD+'..HEAD')
    assert set(git('diff','HEAD','--name-only').decode().splitlines())=={'docs/development/PROJECT_KNOWLEDGE.md','scripts/chat_lifecycle_direct_lab_test.go'}
    assert git('ls-files','--others','--exclude-standard').decode().splitlines()==['scripts/chat_lifecycle_direct_lab_start_integration_test.go']
    return {'at_utc':timestamp(),'files':hashes,'head':HEAD,'tree':TREE,'status':git('status','--porcelain').decode(),'staged':[],'commits':[]}
before=snapshot()
source_manifest=readj(D/'allowlist.json')
for item in source_manifest['files']:
    rel=Path(item['path']); assert not rel.is_absolute() and '..' not in rel.parts
    raw=(D/rel).read_bytes()
    assert len(raw)==item['bytes'] and digest(raw)==item['sha256'],item['path']
for line in (D/'SHA256SUMS').read_text().splitlines():
    sha,rel=line.split('  ',1)
    assert digest((D/rel).read_bytes())==sha,rel
old=git('show',HEAD+':scripts/chat_lifecycle_direct_lab_test.go').decode()
start=old.index('func '+NAME+'(')
end=old.index('\nfunc ',start+5)
old_function=old[start:end].rstrip()+'\n'
assert old_function==(D/'original-test-function.go.txt').read_text()
assert old_function.count('\n')==107
assert old[start:end+1].count('\n')==108
assert old[:start]+old[end+1:]==(R/'scripts/chat_lifecycle_direct_lab_test.go').read_text()
new=(R/'scripts/chat_lifecycle_direct_lab_start_integration_test.go').read_text()
assert new.startswith('//go:build integration\n') and new.count('func '+NAME+'(')==1
new_function=new[new.index('func '+NAME+'('):]
added='''\t// Start does not invoke these tools; keep accidental calls observable without
\t// compiling unused deployment helpers in this controlled process fixture.
\tunusedTool := filepath.Join(directory, "unused-control-tool")
\twriteDirectLabExecutable(t, unusedTool, `#!/usr/bin/env bash
printf 'unexpected-unused-control-tool\\n' >>"$WK_TEST_CALL_LOG"
exit 96
`)
'''
assert new_function.count(added)==1
normalized=new_function.replace(added,'').replace('\t\t"WK_CHAT_LAB_GATE_TOOL="+unusedTool,\n','').replace('\t\t"WK_CHAT_LAB_BUNDLE_TOOL="+unusedTool,\n','')
assert normalized==old_function
raw_script=git('show',HEAD+':scripts/chat-lifecycle/direct-lab.sh')
assert raw_script==(R/'scripts/chat-lifecycle/direct-lab.sh').read_bytes()
assert b'if [[ -z "${WK_CHAT_LAB_GATE_TOOL:-}" ]]' in raw_script
assert b'if [[ -z "${WK_CHAT_LAB_BUNDLE_TOOL:-}" ]]' in raw_script
A=readj(D/'baseline-first/artifact.json');B=readj(D/'variant-first/artifact.json')
commands=[readj(D/n/'command.json') for n in ['baseline-first','variant-first']]
assert commands[0]['argv']==commands[1]['argv']
assert commands[0]['source_sha256']==commands[1]['source_sha256']==digest((D/'fixture_test.go').read_bytes())
assert commands[0]['single_variable']=={'DIRECT_LAB_INJECT_UNUSED_TOOLS':'false'}
assert commands[1]['single_variable']=={'DIRECT_LAB_INJECT_UNUSED_TOOLS':'true'}
expected='build\nmaterialize\nquote\nselector-from-plan\nrelease-enable\nacquire\nrelease-enable-after-provision\nselector\n'
assert A['original_assertions_passed'] and B['original_assertions_passed']
assert A['operation_calls']==B['operation_calls']==expected
assert A['files']==B['files']
for name in ['run-policy.json','materialize-template.json','run-plan.json','quote.json','receipt.json','release-selector.json','diagnostic_ed25519','deployment_ed25519']:
    assert not A['files'][name]['directory'] and int(A['files'][name]['mode'],8)&0o077==0
ja=copy.deepcopy(A['json_files']); jb=copy.deepcopy(B['json_files'])
for field in ['created_at','activated_at']:
    ja['state.json'].pop(field); jb['state.json'].pop(field)
assert ja==jb
starts=[x for x in A['go_calls'] if x['event']=='start'];exits=[x for x in A['go_calls'] if x['event']=='exit']
assert len(starts)==len(exits)==2 and B['go_calls']==[]
assert [Path(x['argv'][-1]).name for x in starts]==['wkcloudgate','wkcloudbundle']
assert all(x['argv'][:3]==['build','-trimpath','-o'] for x in starts)
assert all(x['exit_code']==0 for x in exits)
assert [x['argv'] for x in starts]==[x['argv'] for x in exits]
assert 'subprocess.run([os.environ[\'DIRECT_LAB_REAL_GO\'],*sys.argv[1:]])' in (D/'go-trace.py').read_text()
assert set(A['tool_names'])-set(B['tool_names'])=={'wkcloudgate','wkcloudbundle'}
assert set(B['tool_names'])-set(A['tool_names'])==set()
results={}
for n in ['baseline-first','variant-first','default-list-first','integration-list-first','default-focused-first','integration-focused-first']:
    result=readj(D/n/'result.json')
    assert result['exit_code']==(1 if n=='baseline-first' else 0)
    assert not result['timed_out'] and result['cleanup_verified'] and result['cleanup_remaining']==[]
    results[n]=result
assert NAME not in (D/'default-list-first/stdout-stderr.log').read_text()
assert (D/'integration-list-first/stdout-stderr.log').read_text().splitlines().count(NAME)==1
counts={}
for n,want_count,want_named in [('default-focused-first',11,0),('integration-focused-first',15,1)]:
    events=[]
    for line in (D/n/'stdout-stderr.log').read_text().splitlines():
        try: events.append(json.loads(line))
        except json.JSONDecodeError: pass
    terminal=[e for e in events if e.get('Test') and '/' not in e['Test'] and e.get('Action') in ['pass','fail','skip']]
    passes=[e for e in terminal if e['Action']=='pass']
    assert len(terminal)==len(passes)==want_count
    assert sum(e['Test']==NAME for e in passes)==want_named
    counts[n]={'top_level_passes':len(passes),'failures':0,'skips':0,'named_target_passes':want_named}
outputs={'source_payloads_verified':len(source_manifest['files']),'source_checksums_verified':len((D/'SHA256SUMS').read_text().splitlines()),'target_function_lines':107,'removed_lines_including_separator':108,'other_default_source_byte_identical':True,'target_body_byte_identical_after_only_declared_additions_removed':True,'unchanged_real_script_and_existing_seams':True,'same_serial_scratch_and_only_declared_variant_input':True,'original_eight_operations_presence_modes_json_preserved':True,'json_normalization_only':['state.json.created_at','state.json.activated_at'],'real_go_builds_baseline':2,'real_go_builds_variant':0,'local_fixture_delta_seconds':A['fixture_elapsed_s']-B['fixture_elapsed_s'],'timing_scope':'one local sequential A/B, not Linux gate recovery or compilation-only attribution','results':results,'focused_counts_from_original_event_logs':counts,'no_new_tests_builds_network_or_ci_executed':True}
(O/'independent-verification.json').write_text(json.dumps(outputs,indent=2)+'\n')
after=snapshot()
assert before['files']==after['files']
extra_inputs=[D/'allowlist.json',D/'SHA256SUMS',D/'review-source-freeze.json',D/'plan-and-failure-matrix.md',D/'README.md',Path('/Users/tt/.agents/skills/code-review/SKILL.md'),R/'CONTEXT.md']
source_binding={'review_axis':'Spec','standards_report_read':False,'before':before,'after':after,'inputs':[{'path':str(p),'bytes':p.stat().st_size,'sha256':digest(p.read_bytes())} for p in extra_inputs], 'source_payloads_verified':source_manifest['files'],'applicable_flow':'No root/ancestor or scripts-root scoped FLOW; scripts/cloud-deployment/FLOW.md does not cover these files.'}
(O/'source-binding.json').write_text(json.dumps(source_binding,indent=2)+'\n')
print(json.dumps({'status':'pass','source_payloads_verified':len(source_manifest['files']),'body_equality':True,'default_passes':11,'integration_passes':15,'source_before_after_equal':True}))
