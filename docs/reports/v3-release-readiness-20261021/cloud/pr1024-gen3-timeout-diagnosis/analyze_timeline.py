#!/usr/bin/env python3
"""Offline-only event reduction; never executes a test or source program."""
import json, pathlib, datetime, re, hashlib, collections, csv, io, zipfile
B=pathlib.Path('/tmp/wukongim-v3-readiness-20261021/cloud')
OUT=B/'pr1024-gen3-timeout-diagnosis'
PKG='github.com/WuKongIM/WuKongIM/scripts'
SPEC=[(1,12371361340,119410167925),(2,12373881817,119428117999),(3,12378075592,119447138157)]

def ns(s):
 a,f=re.fullmatch(r'(.+?)(?:\.(\d+))?Z',s).groups()
 dt=datetime.datetime.fromisoformat(a).replace(tzinfo=datetime.timezone.utc)
 return int(dt.timestamp())*10**9+int((f or '').ljust(9,'0'))
def sec(x):return round(x/1e9,9)
def load(p):return json.loads(p.read_text())
def write(p,j):p.write_text(json.dumps(j,ensure_ascii=False,indent=2)+'\n')
rows={};generation=[];input_files=[]
for g,a,j in SPEC:
 C=B/f'pr1024-ci-generation-{g}/candidate';D=C/f'artifacts-public/{a}'
 paths=[D/'test-events.json',D/'members/command.json',D/'members/result.json',D/'members/stdout-stderr.log',C/f'job-logs-public/{j}/complete.log',C/f'job-logs-public/{j}/job.json']
 for p in paths:input_files.append({'path':str(p),'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()})
 zpath=(B/'pr1024-ci-generation-1-private'/f'{a}.zip') if g==1 else (B/f'pr1024-ci-generation-{g}/candidate-private'/f'{a}.zip')
 input_files.append({'path':str(zpath),'bytes':zpath.stat().st_size,'sha256':hashlib.sha256(zpath.read_bytes()).hexdigest(),'use':'Extract safe event fields only; no raw Output retained'})
 with zipfile.ZipFile(zpath) as z:
  assert z.testzip() is None
  raw=z.read('stdout-stderr.log').decode()
 events=[];package=[]
 result=load(paths[2]);job=load(paths[-1]);step=next(s for s in job['steps'] if s['name']=='Run frozen scripts-integration named check once')
 for line in raw.splitlines():
  try:e=json.loads(line)
  except (ValueError,TypeError):continue
  if not isinstance(e,dict):continue
  safe={k:e[k] for k in ['Time','Action','Package','Test','Elapsed'] if k in e}
  if e.get('Action') in ['run','pause','cont','pass','fail','skip'] and e.get('Test'):events.append(safe)
  if e.get('Action') in ['start','pass','fail','skip'] and 'Test' not in e:package.append(safe)
 write(OUT/f'generation-{g}-safe-events.json',events)
 pkgstart=next(e['Time'] for e in package if e['Package']==PKG and e['Action']=='start')
 pkgend=next((e for e in package if e['Package']==PKG and e['Action'] in ['pass','fail','skip']),None)
 emitted=None
 for line in paths[4].read_text().splitlines():
  if 'elapsed_seconds' in line and '"session_id": '+str(result['session_id']) in line:
   emitted=line.split()[0]
 assert emitted
 lookup=collections.defaultdict(list)
 for e in events:lookup[(e['Package'],e['Test'])].append(e)
 recs=[];cont_rank=0
 for (p,t),ee in lookup.items():
  run=next(e['Time'] for e in ee if e['Action']=='run');pauses=[e['Time'] for e in ee if e['Action']=='pause'];conts=[e['Time'] for e in ee if e['Action']=='cont'];terminal=next((e for e in ee if e['Action'] in ['pass','fail','skip']),None)
  # Observable active segments exclude explicit PAUSE->CONT only. They include
  # child joins and process scheduling waits; they are not CPU/slot ownership.
  intervals=[];beg=None;waits=[];waitbeg=None
  for e in ee:
   if e['Action'] in ['run','cont']:
    beg=ns(e['Time'])
    if waitbeg is not None:waits.append((waitbeg,beg));waitbeg=None
   elif e['Action']=='pause':
    if beg is not None:intervals.append((beg,ns(e['Time'])));beg=None
    waitbeg=ns(e['Time'])
   elif e['Action'] in ['pass','fail','skip']:
    if beg is not None:intervals.append((beg,ns(e['Time'])));beg=None
  # For incomplete tests, last event is a proven lower observation bound.
  observed_through=ns(events[-1]['Time'])
  if beg is not None:intervals.append((beg,observed_through))
  if waitbeg is not None:waits.append((waitbeg,observed_through))
  r={'package':p,'test':t,'root':not '/' in t,'run_at':run,'run_offset_s':sec(ns(run)-ns(pkgstart)),'pause_at':pauses,'cont_at':conts,'last_action':ee[-1]['Action'],'terminal_at':terminal['Time'] if terminal else None,'status':terminal['Action'] if terminal else 'incomplete','reported_elapsed_s':terminal.get('Elapsed') if terminal else None,'observed_nonpaused_s':sec(sum(b-a for a,b in intervals)),'explicit_pause_wait_s':sec(sum(b-a for a,b in waits)),'intervals_relative_package_s':[[sec(a-ns(pkgstart)),sec(b-ns(pkgstart))] for a,b in intervals],'observation_complete':terminal is not None}
  if conts:r['cont_offset_s']=sec(ns(conts[0])-ns(pkgstart))
  recs.append(r)
 roots=[r for r in recs if r['root'] and r['package']==PKG]
 for rank,r in enumerate(sorted([r for r in roots if r['cont_at']],key=lambda r:r['cont_at'][0]),1):r['cont_rank']=rank
 rows[g]={(r['package'],r['test']):r for r in recs}
 contfirst=min((e['Time'] for e in events if e['Action']=='cont' and e['Package']==PKG),default=None)
 generation.append({'generation':g,'artifact_id':a,'job_id':j,'command':load(paths[1]),'result':result,'step_start_api_s':step['started_at'],'step_end_api_s':step['completed_at'],'package_start':pkgstart,'package_terminal':pkgend,'last_test_event':events[-1],'result_emitted_at':emitted,'step_to_package_start_s_approx':sec(ns(pkgstart)-ns(step['started_at'])),'package_start_to_last_test_event_s':sec(ns(events[-1]['Time'])-ns(pkgstart)),'package_start_to_result_emission_s':sec(ns(emitted)-ns(pkgstart)),'first_parallel_cont_at':contfirst,'package_start_to_first_parallel_cont_s':sec(ns(contfirst)-ns(pkgstart)) if contfirst else None,'root_counts':dict(collections.Counter(r['status'] for r in recs if r['root'])),'scripts_root_counts':dict(collections.Counter(r['status'] for r in roots)),'root_pending_last_action':dict(collections.Counter(r['last_action'] for r in roots if r['status']=='incomplete'))})
 write(OUT/f'generation-{g}-test-intervals.json',recs)
 write(OUT/f'generation-{g}-package-events.json',package)
comparisons=[]
for key in sorted(set(rows[1])|set(rows[2])|set(rows[3])):
 rr={str(g):rows[g].get(key) for g in rows}
 r={'package':key[0],'test':key[1],'root':not '/' in key[1],'generations':rr}
 if rr['2'] and rr['3'] and rr['2']['observation_complete'] and rr['3']['observation_complete']:
  r['gen3_minus_gen2_reported_s']=round(rr['3']['reported_elapsed_s']-rr['2']['reported_elapsed_s'],3)
  r['gen3_minus_gen2_nonpaused_s']=round(rr['3']['observed_nonpaused_s']-rr['2']['observed_nonpaused_s'],9)
 comparisons.append(r)
write(OUT/'generations.json',generation);write(OUT/'all-test-comparison.json',comparisons);write(OUT/'input-files.json',input_files)
for g in generation:print('GEN',g['generation'],{k:v for k,v in g.items() if k in ['step_to_package_start_s_approx','package_start_to_last_test_event_s','package_start_to_first_parallel_cont_s','root_counts','scripts_root_counts','root_pending_last_action']})
print('TOP REPORTED ELAPSED COMMON ROOT INCREASE: g1/g2/g3')
for r in sorted((r for r in comparisons if r['root'] and 'gen3_minus_gen2_reported_s' in r),key=lambda r:r['gen3_minus_gen2_reported_s'],reverse=True)[:30]:print(r['test'],*[r['generations'][str(g)]['reported_elapsed_s'] if r['generations'][str(g)] else None for g in (1,2,3)],'delta',r['gen3_minus_gen2_reported_s'])
for g in (1,2,3):
 print('TOP G',g)
 for r in sorted((r for r in rows[g].values() if r['root']),key=lambda r:r['reported_elapsed_s'] or 0,reverse=True)[:12]:print(r['test'],r['reported_elapsed_s'],r.get('cont_offset_s'),r['run_offset_s'])
phase_rows=[]
for gen in generation:
 g=gen['generation'];root=[r for r in rows[g].values() if r['root'] and r['package']==PKG]
 pc=min(r['cont_offset_s'] for r in root if r['cont_at'])
 sh=next(r for r in root if r['test']=='TestChatLifecycleShakeoutScriptIntegration')
 co=next(r for r in root if r['test']=='TestChatLifecycleTerminalEvidenceRetention')
 phase_rows.append({'generation':g,'outer_result':gen['result']['status'],'outer_elapsed_with_cleanup_s':gen['result']['elapsed_seconds'],'pre_package_marker_s_approx':gen['step_to_package_start_s_approx'],'package_marker_to_first_root_cont_s':pc,'serial_shakeout_span_s':sh['observed_nonpaused_s'],'remaining_prefix_span_s':round(pc-sh['observed_nonpaused_s'],9),'api_step_to_root_parallel_phase_s_approx':round(gen['step_to_package_start_s_approx']+pc,9),'nominal_600_minus_api_step_to_root_parallel_phase_s_approx':round(600-gen['step_to_package_start_s_approx']-pc,9),'parallel_root_phase_to_last_observed_event_s':round(gen['package_start_to_last_test_event_s']-pc,9),'package_start_to_result_emission_s':gen['package_start_to_result_emission_s'],'collector_cont_root_rank':co['cont_rank'],'collector_explicit_pause_s':co['explicit_pause_wait_s'],'collector_reported_elapsed_s':co['reported_elapsed_s'],'collector_last_observed_bound_nonpaused_s':co['observed_nonpaused_s'],'roots':gen['root_counts'],'pending_states':gen['root_pending_last_action']})
write(OUT/'phase-timing-table.json',{'rows':phase_rows,'method':'Disjoint wall-clock intervals. Root CONT phase follows all top-level registrations; early child CONTs are not this boundary. Prefix minus contained serial shakeout is interval subtraction, not summed test durations. API step start second precision; no exact process start inferred. Incomplete observed intervals end at final safe event, not assumed alive until result emission.'})
appendix=[]
for g in (1,2):
 p=B/f'pr1024-ci-generation-{g}/candidate/scripts-first-failure-analysis.json';j=load(p)
 old={(e['Package'],e['Test']) for e in j['root_terminal_events'] if e['Action']=='pass'}
 new={(k[0],k[1]) for k,r in rows[g].items() if r['root'] and r['status']=='pass'}
 appendix.append({'generation':g,'original_summary_path':str(p),'original_summary_sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'original_summary_root_counts':j['root_counts'],'corrected_root_counts':generation[g-1]['root_counts'],'missing_from_old_sanitized_summary':[{'package':p,'test':t} for p,t in sorted(new-old)],'reason':'Old summary parsed conservatively sanitized stdout; harmless test names matched token/secret/authorization redactor. No original bytes changed; this appendix uses only safe event fields extracted from original verified ZIP.'})
write(OUT/'count-correction-appendix.json',appendix)
