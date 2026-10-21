import ast, hashlib, importlib.util, json, pathlib, re, subprocess, sys, tempfile
ROOT=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-preview-fixture')
E=pathlib.Path('/tmp/wukongim-v3-readiness-20261021/packages/provider-preview-fixture-implementation')
O=pathlib.Path(__file__).parent
BASE='be32eb375e3c77dc5b3d1070fd798c281ae2d3bc'
def sha(raw):return hashlib.sha256(raw).hexdigest()
def facts(p):
 raw=p.read_bytes();return {'size':len(raw),'sha256':sha(raw)}
def save(name,v):(O/name).write_text(json.dumps(v,indent=2,sort_keys=True)+'\n')
def git(*args):return subprocess.check_output(['git',*args],cwd=ROOT)
assert sha((E/'source-freeze.json').read_bytes())=='50515387f78397c37570dcb3fe6f5b0fc254851df3817105d2fa633ce6892155'
assert sha((E/'implementation.patch').read_bytes())=='7c1aadc1e94c5de01ccfbc4e818b159ffdfbf4a75dcdfcc822485ce2fd2b8929'
f=json.loads((E/'source-freeze.json').read_bytes()); files=[x['path'] for x in f['source_files']]
assert git('rev-parse','HEAD').decode().strip()==BASE
assert git('rev-parse','HEAD^{tree}').decode().strip()=='e319ee72e8d30f012fd5e6206d131e2b75c47a09'
tracked=git('diff','--name-only',BASE).decode().splitlines(); untracked=git('ls-files','--others','--exclude-standard').decode().splitlines()
assert set(files)==set(tracked+untracked) and len(files)==7
before={p:facts(ROOT/p) for p in files}
for entry in f['source_files']:
 p=entry['path'];assert before[p]=={'sha256':entry['sha256'],'size':entry['bytes']};assert (ROOT/p).read_bytes()==(E/'final-source'/p).read_bytes()
unchanged=[]
for p in git('ls-files').decode().splitlines():
 if p not in tracked:
  assert (ROOT/p).read_bytes()==git('show',BASE+':'+p),p
  unchanged.append(p)
w='.github/workflows/native-package-repository-drill.yml'; new=(ROOT/w).read_text()
for name in ['Verify native TEST ONLY preview and stable delivery fixtures','Preserve public native delivery fixture evidence']:
 pattern=r'^      - name: '+re.escape(name)+r'\n.*?(?=^      - name:|^  [a-z]|\Z)'
 new,n=re.subn(pattern,'',new,flags=re.M|re.S);assert n==1
assert new.encode()==git('show',BASE+':'+w)
sys.path[:0]=[str(ROOT),str(ROOT/'tests')]
import native_stable_candidate_drill as N
import native_stable_clients_drill as K
out=[]
with tempfile.TemporaryDirectory(prefix='spec-helper-',dir=O) as t:
 p=pathlib.Path(t); dest=p/'out';dest.mkdir(); binary=p/'binaries'
 def rejected(name,call,reason):
  try:call()
  except (RuntimeError,ValueError) as e:
   assert reason in str(e),(name,str(e));out.append({'case':name,'exception':type(e).__name__,'message':str(e),'passed':True})
  else:raise AssertionError(name+' unexpectedly accepted')
 args=dict(output_root=dest,binaries_root=binary,trusted_root=p/'unused-trusted',product_source_sha='fc18221282f289088ec9b4282fda8e0bfc67a3b8')
 rejected('missing binary root',lambda:N.export_client_generations(**args),'binary generation root')
 for version in ['3.0.0','3.0.1']:
  d=binary/version;d.mkdir(parents=True)
  for name in ['wukongim','wkcli']:
   x=d/name;x.write_bytes(b'TEST ONLY malformed ELF\n'*4);x.chmod(0o755)
 rejected('malformed ELF',lambda:N.export_client_generations(**args),'ELF')
 rejected('wrong fixed source',lambda:N.export_client_generations(**{**args,'product_source_sha':'1'*40}),'fixed product source')
 assert not list(dest.iterdir())
 mod=K.module('scripts/verify-object-storage-delivery-inputs.py'); inputs=p/'inputs.json';inputs.write_text('{"test_only":true}')
 rejected('fresh production rejects explicit TEST ONLY',lambda:mod.verify_delivery_inputs(inputs=inputs,expected_plan_sha256='0'*64,executor_sha=BASE),'fresh delivery input authentication failed')
assert before=={p:facts(ROOT/p) for p in files}
save('helper-probe.json',{'passed':True,'cases':out,'native_tools_or_network_invoked':False,'scratch_removed':True})
result=json.loads((E/'ci-init-native-1/result.json').read_bytes()); command=json.loads((E/'ci-init-native-1-command.json').read_bytes())
tree=ast.parse((ROOT/'tests/native_object_delivery_fixture_drill.py').read_text())
cases=next(ast.literal_eval(n.value) for n in tree.body if isinstance(n,ast.Assign) and any(isinstance(v,ast.Name) and v.id=='CASES' for v in n.targets))
assert len(cases)==29 and len(result['cases'])==29 and {c['case'] for c in result['cases']}==set(cases)
assert result['passed'] and result['scratch_removed'] and result['failure'] is None
assert all(c['passed'] for c in result['cases'])
assert all(not c['timed_out'] and c['exit_code']==0 for c in result['export']['commands'])
assert all(not c['timed_out'] and not c['output_limit'] for c in result['commands'])
assert command['exit_code']==command['cleanup_exit_code']==0 and not command['timed_out']
assert command['container_state_before_cleanup']['Running'] is False
assert command['container_state_before_cleanup']['OOMKilled'] is False
rows=[]
for c in result['commands']:
 expected=1 if c['stage'].startswith('reject-') else 0
 assert c['exit_code']==expected,(c['stage'],c['exit_code'])
 raw=c['output_tail'].encode(); complete=len(raw)==c['output_size']
 if complete:assert sha(raw)==c['output_sha256']
 rows.append({'stage':c['stage'],'exit_code':c['exit_code'],'full_output_hash_independently_verified':complete,'output_tail':c['output_tail'] if expected==1 else None})
save('receipt-check.json',{'source':facts(E/'ci-init-native-1/result.json'),'command':facts(E/'ci-init-native-1-command.json'),'case_inventory':list(cases),'export_commands':[{k:c[k] for k in ['stage','exit_code','timed_out']} for c in result['export']['commands']],'drill_commands':rows,'cleanup_exit_code':0,'verification':result['verification'],'local_equivalence_only':True,'native_suite_not_rerun':True})
rules={str(p):facts(p) for p in [ROOT.parents[1]/'AGENTS.md',ROOT.parents[1]/'.github/workflows/README.md']}
save('source-binding.json',{'base':BASE,'head':BASE,'tree':f['base_tree'],'commit_list':[],'diff_commands':['git diff '+BASE,'git ls-files --others --exclude-standard'],'source_freeze':facts(E/'source-freeze.json'),'implementation_patch':facts(E/'implementation.patch'),'before':before,'after':{p:facts(ROOT/p) for p in files},'untracked':untracked,'unchanged_tracked_count':len(unchanged),'unchanged_tracked_paths':unchanged,'legacy_workflow_exact_after_removing_two_new_steps':True,'applicable_rules':rules,'packages_AGENTS_FLOW_CONTEXT':[],'specs':{str(p):facts(p) for p in [E/'failure-matrix.md',E/'ci-scope-addendum.md',E.parent/'provider-after-d2-plan/README.md',E.parent/'provider-after-d2-plan-review/report.md']}})
print(json.dumps({'source_files':len(files),'unchanged_tracked':len(unchanged),'helper_probes':len(out),'real_native_receipt_cases_checked':len(cases),'commands_checked':len(rows)+len(result['export']['commands']),'source_unchanged':True}))
