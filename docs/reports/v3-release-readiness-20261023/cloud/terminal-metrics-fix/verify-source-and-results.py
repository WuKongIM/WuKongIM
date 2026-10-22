import json,hashlib,ast,subprocess
from pathlib import Path
s=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-metrics');o=Path('/tmp/wukongim-v3-readiness-20261023/cloud/terminal-metrics-fix')
paths=['CHANGELOG.md','docs/development/PROJECT_KNOWLEDGE.md','scripts/chat-lifecycle/terminal-evidence.py','scripts/testdata/chat_lifecycle_terminal_evidence_test.py']
actual=subprocess.check_output(['git','diff','--name-only'],cwd=s,text=True).splitlines();assert sorted(actual)==sorted(paths)
prior=subprocess.check_output(['git','show','HEAD:scripts/chat-lifecycle/terminal-evidence.py'],cwd=s,text=True);current=(s/'scripts/chat-lifecycle/terminal-evidence.py').read_text()
def constant(node):
 if isinstance(node,ast.BinOp) and isinstance(node.op,ast.LShift):return ast.literal_eval(node.left)<<ast.literal_eval(node.right)
 return ast.literal_eval(node)
def constants(body):
 return {n.targets[0].id:constant(n.value) for n in ast.parse(body).body if isinstance(n,ast.Assign) and len(n.targets)==1 and isinstance(n.targets[0],ast.Name) and n.targets[0].id in ['MAX_FILE_BYTES','METRIC_TOTAL_BYTES','METRIC_DEADLINE_SECONDS','METRIC_PARTITIONS']}
a,b=constants(prior),constants(current)
for k in ['MAX_FILE_BYTES','METRIC_TOTAL_BYTES','METRIC_DEADLINE_SECONDS']:assert a[k]==b[k]
assert len(a['METRIC_PARTITIONS'])==len(b['METRIC_PARTITIONS'])==8
assert a['METRIC_PARTITIONS'][:5]==b['METRIC_PARTITIONS'][:5]
def functions(body):return {n.name:ast.dump(n,include_attributes=False) for n in ast.parse(body).body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef))}
assert functions(prior)==functions(current)
integration=json.loads((o/'integration-first-green-artifact.json').read_text());assert integration['tests_run']==23 and integration['failure_count']==0 and integration['error_count']==0
fixture=next(x for x in integration['cases'] if x['case']=='registered-resource-families')
resource_summary={}
for row in fixture['manifest']['captures']:
 if row['name'] in ['range-process','range-counters','range-go']:resource_summary[row['name']]={k:row[k] for k in ['query','series_count','sample_count','window','sha256']}
(o/'registered-resource-green-summary.json').write_text(json.dumps(resource_summary,indent=2)+'\n')
(o/'final-diff.patch').write_bytes(subprocess.check_output(['git','diff','--binary'],cwd=s))
final={'head':subprocess.check_output(['git','rev-parse','HEAD'],cwd=s,text=True).strip(),'tree':subprocess.check_output(['git','rev-parse','HEAD^{tree}'],cwd=s,text=True).strip(),'changed_paths':paths,'changed_sha256':{p:hashlib.sha256((s/p).read_bytes()).hexdigest() for p in paths},'rules_sha256':hashlib.sha256((s/'AGENTS.md').read_bytes()).hexdigest(),'unchanged_budget_constants':{k:v for k,v in b.items() if k!='METRIC_PARTITIONS'},'fixed_partition_count':8,'first_five_partitions_unchanged':True,'all_helper_function_asts_unchanged':True,'workflow_policy_skill_changes':False,'source_dirty_only_four_intended_paths':True,'test_methods':integration['tests_run'],'fixture_cases':len(integration['cases']),'first_full_python_seconds':json.loads((o/'integration-first-green-receipt.json').read_text())['elapsed_seconds'],'commit_push_PR_performed':False,'cloud_SSH_MCP_operations_performed':False}
(o/'final-source-binding.json').write_text(json.dumps(final,indent=2)+'\n')
print(json.dumps(final,indent=2))
