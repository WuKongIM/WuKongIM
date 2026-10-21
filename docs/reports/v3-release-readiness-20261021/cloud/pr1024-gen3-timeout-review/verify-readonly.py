"""Recompute the frozen evidence using safe fields only; no test execution."""
from pathlib import Path
from datetime import datetime,timezone
from collections import Counter,defaultdict
import hashlib,json,re,zipfile,subprocess
root=Path('/tmp/wukongim-v3-readiness-20261021/cloud')
p=root/'pr1024-gen3-timeout-diagnosis';out=Path(__file__).resolve().parent
repo=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM')
head='44de9452d94edae16129b5781d794f8c1c6c7c57'
def sha(b):return hashlib.sha256(b).hexdigest()
def load(f):return json.loads(f.read_text())
def ns(text):
 match=re.fullmatch(r'(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?Z',text)
 return int(datetime.fromisoformat(match[1]).replace(tzinfo=timezone.utc).timestamp())*10**9+int((match[2] or '').ljust(9,'0'))
def sec(value):return value/10**9
def equal(a,b):assert abs(a-b)<1e-8,(a,b)
manifest=load(p/'allowlist.json');assert sha((p/'allowlist.json').read_bytes())=='9debf2d669b77cb2565e7af066eee2f94ab2f44c907a2f72b69f6202f9fee072'
binding=[]
for item in manifest['files']:
 f=p/item['path'];assert f.is_file() and not f.is_symlink()
 b=f.read_bytes();assert len(b)==item['bytes'] and sha(b)==item['sha256'],f
 binding.append(item)
for item in load(p/'input-files.json'):
 b=Path(item['path']).read_bytes();assert len(b)==item['bytes'] and sha(b)==item['sha256'],item['path']
source=load(p/'source-freeze.json');sources=[]
for item in source['files']:
 data=subprocess.check_output(['git','show',f'{head}:{item["path"]}'],cwd=repo)
 blob=subprocess.check_output(['git','rev-parse',f'{head}:{item["path"]}'],cwd=repo,text=True).strip()
 assert sha(data)==item['sha256'] and blob==item['git_blob'],item['path']
 sources.append(item)
rows=[];raw_by_gen={};roots_by_gen={};package='github.com/WuKongIM/WuKongIM/scripts'
tables=load(p/'phase-timing-table.json')['rows'];generations=load(p/'generations.json')
for gen,aid in [(1,12371361340),(2,12373881817),(3,12378075592)]:
 base=root/f'pr1024-ci-generation-{gen}'/'candidate'
 zp=root/'pr1024-ci-generation-1-private'/f'{aid}.zip' if gen==1 else base.parent/'candidate-private'/f'{aid}.zip'
 receipt=load(base/f'artifacts-public/{aid}/receipt.json')
 digest=sha(zp.read_bytes());assert digest==receipt['observed_sha256']
 with zipfile.ZipFile(zp) as z:
  assert z.testzip() is None
  raw=z.read('stdout-stderr.log');originals=[]
  for line in raw.splitlines():
   try:x=json.loads(line)
   except ValueError:continue
   if isinstance(x,dict):originals.append(x)
 safe=[{k:x[k] for k in ['Time','Action','Package','Test','Elapsed'] if k in x} for x in originals if x.get('Test') and x['Action'] in ['run','pause','cont','pass','fail','skip']]
 assert safe==load(p/f'generation-{gen}-safe-events.json')
 roots=defaultdict(list)
 for e in safe:
  if '/' not in e['Test']:roots[(e['Package'],e['Test'])].append(e)
 counts=Counter(v[-1]['Action'] if v[-1]['Action'] in ['pass','fail','skip'] else 'incomplete' for v in roots.values())
 assert dict(counts)==generations[gen-1]['root_counts']
 roots_by_gen[gen]=roots;raw_by_gen[gen]=safe
 start=next(e['Time'] for e in originals if e.get('Package')==package and e['Action']=='start' and not e.get('Test'))
 allcont=next(e for e in safe if e['Action']=='cont')
 packagecont=next(e for e in safe if e['Action']=='cont' and e['Package']==package)
 rootcont=[e for e in safe if e['Action']=='cont' and e['Package']==package and '/' not in e['Test']]
 first=rootcont[0]
 collector_name='TestChatLifecycleTerminalEvidenceRetention'
 ce=roots[(package,collector_name)];cr=next(e for e in ce if e['Action']=='cont');pause=next(e for e in ce if e['Action']=='pause')
 rank=1+next(i for i,e in enumerate(rootcont) if e['Test']==collector_name)
 shake=roots[(package,'TestChatLifecycleShakeoutScriptIntegration')]
 assert len([e for e in shake if e['Action'] in ['pause','cont']])==0
 assert ns(shake[0]['Time'])>=ns(start) and ns(shake[-1]['Time'])<=ns(first['Time'])
 shake_elapsed=sec(ns(shake[-1]['Time'])-ns(shake[0]['Time']))
 row={'generation':gen,'original_zip_sha256':digest,'original_stdout_sha256':sha(raw),'safe_events_count':len(safe),'all_package_first_cont':allcont,'scripts_package_first_any_cont':packagecont,'scripts_first_root_cont':first,'package_start':start,'step_start_second_precision':generations[gen-1]['step_start_api_s'],'step_to_marker_s_approx':sec(ns(start)-ns(generations[gen-1]['step_start_api_s'])),'marker_to_root_cont_s':sec(ns(first['Time'])-ns(start)),'shakeout_contained_span_s':shake_elapsed,'roots':dict(counts),'collector_root_cont_rank':rank,'collector_explicit_pause_s':sec(ns(cr['Time'])-ns(pause['Time'])),'collector_terminal':[e for e in ce if e['Action'] in ['pass','fail','skip']],'pending_states':dict(Counter(v[-1]['Action'] for k,v in roots.items() if v[-1]['Action'] not in ['pass','fail','skip']))}
 expected=tables[gen-1]
 equal(row['step_to_marker_s_approx'],expected['pre_package_marker_s_approx']);equal(row['marker_to_root_cont_s'],expected['package_marker_to_first_root_cont_s']);equal(shake_elapsed,expected['serial_shakeout_span_s']);equal(row['collector_explicit_pause_s'],expected['collector_explicit_pause_s'])
 assert row['collector_root_cont_rank']==expected['collector_cont_root_rank'] and row['pending_states']==expected['pending_states']
 rows.append(row)
corrections=[]
for old in load(p/'count-correction-appendix.json'):
 oldpath=Path(old['original_summary_path']);assert sha(oldpath.read_bytes())==old['original_summary_sha256']
 summary=load(oldpath);oldset={(x['Package'],x['Test']) for x in summary['root_terminal_events'] if x['Action']=='pass'}
 newset={k for k,v in roots_by_gen[old['generation']].items() if v[-1]['Action']=='pass'}
 missing=[{'package':k[0],'test':k[1]} for k in sorted(newset-oldset)]
 assert missing==old['missing_from_old_sanitized_summary'] and len(missing)==8
 assert oldset<=newset and old['corrected_root_counts']==rows[old['generation']-1]['roots']
 corrections.append({'generation':old['generation'],'missing_names_confirmed':missing,'old_summary_sha256':old['original_summary_sha256'],'old_bytes_unchanged':True})
a,b=rows[1],rows[2]
delta_pre=b['step_to_marker_s_approx']-a['step_to_marker_s_approx'];delta_prefix=b['marker_to_root_cont_s']-a['marker_to_root_cont_s']
equal(delta_pre+delta_prefix,32.307137883)
# Observe safe parent/first-child intervals without labelling them lock-acquisition timings.
lock_rows=[]
safe=raw_by_gen[3]
for name in ['TestCloudSimulationSetupBoundsAndResumesGitHubCLIDownload','TestCloudSimulationAnalyzeSignalsCloseWindowAndKillDiagnosisGroup','TestCloudSimulationAnalyzeBoundsCodexEnvironmentProbes','TestWukongIMThreeNodeLocalE2EBuildUsesSelectedWorktreeRevision']:
 ev=roots_by_gen[3][(package,name)];cont=next(e for e in ev if e['Action']=='cont');end=ev[-1]
 child=next((e for e in safe if e['Action']=='run' and e['Test'].startswith(name+'/')),None)
 lock_rows.append({'test':name,'root_cont':cont['Time'],'root_terminal':end,'first_child_run':child,'cont_to_first_child_s':sec(ns(child['Time'])-ns(cont['Time'])) if child else None,'interpretation':'CONT precedes the source-defined admission gate; this is not an observed lock acquisition or CPU duration.'})
result={'frozen_input_manifest_sha256':sha((p/'allowlist.json').read_bytes()),'input_payloads':len(binding),'input_payload_bytes':sum(x['bytes'] for x in binding),'all_21_original_input_hashes_verified':len(load(p/'input-files.json')),'exact_head_source_files_verified':len(sources),'generations':rows,'disjoint_growth':{'pre_marker_approx_s':delta_pre,'root_serial_prefix_s':delta_prefix,'sum_approx_s':delta_pre+delta_prefix,'outer_gen2_nominal_margin_s':600-generations[1]['result']['elapsed_seconds'],'no_counterfactual_completion_claim':True},'counts_correction':corrections,'lock_evidence_rows':lock_rows,'limits':['Read-only safe-field extraction; original raw outputs/profiles/ZIPs are not copied.','API step origins have second precision; package start is not the exact package alarm start.','Root CONT differs from child/all-package CONT; admission may follow CONT.','No lock acquisition, compile, readiness, actual workload or drain phase timestamp is independently retained for the highlighted successful fixtures.','No fixture experiment, new test/build/CI/cloud run, budget recommendation or source mutation executed.']}
(out/'independent-verification.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
(out/'source-binding.json').write_text(json.dumps({'head':head,'tree':source['tree'],'reviewed_manifest_sha256':sha((p/'allowlist.json').read_bytes()),'reviewed_files':binding,'exact_git_sources':sources,'rules':{'AGENTS.md_sha256':next(x['sha256'] for x in sources if x['path']=='AGENTS.md'),'applicable_flow':[],'reason':'Evidence reduction and scripts-root source reading only; no package runtime analysis.'},'immutable_before_after':'checked again at seal'},indent=2)+'\n')
print(json.dumps({'reviewed_payloads':len(binding),'source_files':len(sources),'growth_s':delta_pre+delta_prefix,'roots':[r['roots'] for r in rows],'collector_ranks':[r['collector_root_cont_rank'] for r in rows],'result':'all independent checks pass'},indent=2))
