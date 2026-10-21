"""Read-only fixed-source and recorded-result validation; no tests are run."""
from pathlib import Path
import datetime, hashlib, json, subprocess

ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-evidence')
E = Path('/tmp/wukongim-v3-readiness-20261021/cloud/pr1024-pprof-interrupt-repair-v1')
OUT = Path(__file__).parent
BASE = '2cc562fa39f7b430c61a4494636f6ee0ec0d631a'
FILES = ['CHANGELOG.md', 'docs/development/PROJECT_KNOWLEDGE.md', 'scripts/capture-wukongim-local-threshold-pprof.sh', 'scripts/wukongim_local_threshold_pprof_script_integration_test.go']

def sha(raw): return hashlib.sha256(raw).hexdigest()
def git(*args): return subprocess.check_output(['git', '-C', str(ROOT), *args])
def facts(path):
    raw = path.read_bytes()
    return {'bytes': len(raw), 'sha256': sha(raw)}

def source_state():
    return {'head': git('rev-parse', 'HEAD').decode().strip(), 'tree': git('rev-parse', 'HEAD^{tree}').decode().strip(),
            'staged': git('diff', '--cached', '--name-only').decode().splitlines(),
            'untracked': git('ls-files', '--others', '--exclude-standard').decode().splitlines(),
            'changed': git('diff', BASE, '--name-only').decode().splitlines(),
            'files': {name: facts(ROOT/name) for name in FILES}}

before = source_state()
assert before['head'] == BASE and before['tree'] == '85f578ba19135892c3d4f5e18db37329cd515cf7'
assert not before['staged'] and not before['untracked'] and sorted(before['changed']) == sorted(FILES)
freeze_raw = (E/'review-source-freeze.json').read_bytes()
assert sha(freeze_raw) == 'b7c7cc4c1ea841a8f97b0998760c739291f85d5414eabefa38756fcc2d54a351'
freeze = json.loads(freeze_raw)
assert {name: item['sha256'] for name, item in before['files'].items()} == freeze['files']
assert sha((ROOT/'AGENTS.md').read_bytes()) == freeze['rules']['AGENTS.md']
assert not any((ROOT/name).exists() for name in ('FLOW.md', 'scripts/FLOW.md', 'scripts/AGENTS.md', 'docs/AGENTS.md', 'docs/development/AGENTS.md', 'docs/FLOW.md', 'docs/development/FLOW.md'))

old_helper = git('show', BASE + ':' + FILES[2])
assert old_helper == (E/'helper-before.sh').read_bytes()
old_test = git('show', BASE + ':' + FILES[3]).decode()
new_test = (ROOT/FILES[3]).read_text()
start = new_test.index('// Signals must not outrun ownership registration')
end = new_test.index('func localThresholdPprofArgs(', start)
restored = (new_test[:start] + new_test[end:]).replace('\t"fmt"\n','').replace('\t"strconv"\n','')
assert restored == old_test
assert (E/'test-complete-before-helper.go').read_bytes() == (ROOT/FILES[3]).read_bytes()
red_freeze = json.loads((E/'red-complete-freeze.json').read_bytes())
assert red_freeze['helper_sha256'] == sha(old_helper) and red_freeze['test_sha256'] == sha((ROOT/FILES[3]).read_bytes())

all_expected = {(node, signal) for node in (0,1,2) for signal in ('HUP','INT','TERM')}
observed_red = set()
for name in ('red-first-artifact.json', 'red-prelaunch-first-artifact.json'):
    for case in json.loads((E/name).read_bytes())['cases']:
        identity = (case['node'],case['signal'])
        assert identity not in observed_red and case['passed'] is False
        observed_red.add(identity)
assert observed_red == all_expected
cases = json.loads((E/'green-first-artifact.json').read_bytes())['cases']
assert {(c['node'],c['signal']) for c in cases} == all_expected and len(cases) == 9
for case in cases:
    assert case['passed'] and case['helper_exit_code'] == 0 and case['signal_to_exit_seconds'] <= 3
    assert case['active_requests'] == [0,0,0] and case['child_alive'] is False and not case['temporary_files']
    assert case['metadata']['capture']['status'] == 'partial' and case['metadata']['capture']['reason'] == 'interrupted' and case['metadata']['capture']['valid'] is False
    assert case['registered_before_signal'] == (3 if case['node'] == 2 else 0)
events = [json.loads(line) for line in (E/'green-first.jsonl').read_text().splitlines()]
passed = [e for e in events if e['Action'] == 'pass' and 'Test' in e]
assert len(passed) == 38 and sum('/' not in e['Test'] for e in passed) == 15
assert not any(e['Action'] in ('fail','skip') for e in events)
assert events[-1]['Action'] == 'pass' and 'Test' not in events[-1]

names = ['review-source-freeze.json','before-freeze.json','plan-and-failure-matrix.md','README.md','test-first-freeze.json','red-complete-freeze.json','helper-before.sh','test-before.go','test-first.go','test-complete-before-helper.go','red-first.jsonl','red-first-artifact.json','red-prelaunch-first.jsonl','red-prelaunch-first-artifact.json','green-first.jsonl','green-first-artifact.json','delivery-receipt.json']
evidence = {name: facts(E/name) for name in names}
rules = {name: facts(ROOT/name) for name in ('AGENTS.md','CONTEXT.md','.github/workflows/README.md','scripts/wukongim_local_threshold_pprof_script_test.go')}
patch = git('diff', BASE, '--', *FILES)
(OUT/'reviewed.diff').write_bytes(patch)
after = source_state()
assert before == after
assert all(facts(E/name) == item for name,item in evidence.items())
result = {'schema':'wukongim.pprof_interrupt_spec_binding/v1','observed_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
          'axis':'Spec','standards_report_read':False,'base':BASE,'commit_list':[],
          'diff_command':['git','diff',BASE,'--',*FILES],'worktree':str(ROOT),
          'source_before':before,'source_after':after,'source_unchanged':True,'reviewed_patch':facts(OUT/'reviewed.diff'),
          'applicable_FLOW':[],'rules_and_unchanged_static_tests':rules,'code_review_skill':facts(Path('/Users/tt/.agents/skills/code-review/SKILL.md')),
          'evidence_directory':str(E),'evidence_hashes':evidence,'recorded_validation':{'old_helper_exact':True,'all_preexisting_integration_body_bytes_unchanged':True,'complete_new_test_precedes_helper_change':True,'first_red_subcases':9,'first_green_subcases':9,'related_green_top_level_tests':15,'related_green_pass_records':38,'related_green_skips':0,'all_green_requests_zero':True,'all_green_owned_children_gone':True,'all_green_temporary_files_empty':True},
          'review_execution':{'source_mutations':False,'test_reruns':False,'cloud_or_network':False,'all_owned_commands_terminal':True}}
print(json.dumps(result,ensure_ascii=False,indent=2))
