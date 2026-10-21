from pathlib import Path
import json,csv,hashlib,re
p=Path(__file__).parent; private=p.parent/'performance-private'; members=private/'correctness-members';head='44de9452d94edae16129b5781d794f8c1c6c7c57';checkout='c1a832afe71a46294a2e46054e26ea8568f6c315'
def sha(b):return hashlib.sha256(b).hexdigest()
def load(f):return json.loads(f.read_bytes())
def write(name,v):(p/name).write_text(json.dumps(v,ensure_ascii=False,indent=2)+'\n')
records=[]
for artifact in sorted((p/'artifacts-public').iterdir()):
 if not (artifact/'receipt.json').exists():continue
 receipt=load(artifact/'receipt.json');a=receipt['artifact'];root=artifact/'members'
 if not (root/'arrival.json').exists():continue
 r=load(root/'arrival.json');warm=load(root/'arrival.warmup.json');assert r['qualification'] and r['phase']=='measurement' and len(r['windows'])==3 and len(r['seconds'])==180 and len(warm['seconds'])==60
 assert (root/'source.sha').read_text().strip()==checkout
 assert load(root/'result.json')=={'status':'pass'}
 for window in r['windows']:
  assert window['planned']==window['started']==window['completed']==30000 and window['errors']==window['dropped']==0 and window['scheduled_to_completion_p99_ms']<=400
 records.append({'seam':a['name'].split('-c1a')[0].removeprefix('500qps-'),'artifact_id':a['id'],'source':checkout,'windows':r['windows'],'warmup_seconds':len(warm['seconds']),'measurement_seconds':len(r['seconds']),'totals':{key:sum(w[key] for w in r['windows']) for key in ('planned','started','completed','errors','dropped')},'binary_sha256':(root/'binary.sha256').read_text().split()[0],'benchmark_output_truncated_marker':'truncat' in (root/'benchmark.txt').read_text().lower()})
assert len(records)==3
write('performance-window-summary.json',{'run_id':39664872153,'attempt':1,'head':head,'checkout':checkout,'windows':records,'aggregate':{key:sum(r['totals'][key] for r in records) for key in ('planned','started','completed','errors','dropped')},'limits':'P99 is not maximum request latency; hosted hardware is not a controlled same-hardware comparison. Nonverbose unit/race logs do not expose every internal skip.'})
flight=load(p/'artifacts-public/12375579850/members/flight/window.json')
flight_keys=('schema','status','started_at','stopped_at','export_started_at','export_ended_at','trace_bytes','trace_complete','counters_complete','handlers_completed','history_target_seconds','runtime_byte_target','output_byte_limit')
write('flight-summary.json',{k:flight.get(k) for k in flight_keys})
assert (members/'source.sha').read_text().strip()==checkout
integrity=load(p/'correctness-members-integrity.json')
checksum={}
for line in (members/'artifacts/evidence/checksums.sha256').read_text().splitlines():
 digest,name=line.split(None,1);name=name.lstrip('*').strip();checksum[name]=digest
verified=[];public=[]
for row in integrity['selected_members']:
 name=row['path'];raw=(members/name).read_bytes();assert len(raw)==row['bytes'] and sha(raw)==row['sha256']
 if name.startswith('artifacts/') and name!='artifacts/evidence/checksums.sha256':
  relative=name.removeprefix('artifacts/');assert checksum.get(relative)==sha(raw),(name,relative);verified.append(name)
 if name=='driver.log' or '/logs/' in name:continue
 assert not re.search(rb'-----BEGIN (?:OPENSSH|RSA|EC|DSA)? ?PRIVATE KEY|github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,}|[?&](?:sig|signature)=',raw)
 dest=p/'correctness-members'/name;dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes(raw);public.append({'member':name,'public_path':str(dest.relative_to(p)),'bytes':len(raw),'sha256':sha(raw)})
final=load(members/'artifacts/report/final.json');qual=load(members/'artifacts/report/qualification.json');local=load(members/'artifacts/local-step.json')
queues=list(csv.DictReader((members/'artifacts/product_queue_summary.tsv').read_text().splitlines(),delimiter='\t'))
scan=[]
for name in [r['path'] for r in integrity['selected_members'] if '/logs/node' in r['path'] or '/logs/service-' in r['path']]:
 raw=(members/name).read_bytes();matches=[{'line':n,'sha256':sha(line.encode())} for n,line in enumerate(raw.decode().splitlines(),1) if re.search(r'\b(?:WARN|ERROR|panic|error)\b',line)]
 scan.append({'member':name,'bytes':len(raw),'sha256':sha(raw),'matching_lines':matches})
coordinator=[]
for line in (members/'artifacts/logs/coordinator.log').read_text().splitlines():
 try:d=json.loads(line)
 except json.JSONDecodeError:continue
 if isinstance(d,dict) and d.get('messages',{}).get('retry_attempts',0):coordinator.append({'at':d.get('at'),'cut':d.get('cut'),'retry_attempts':d['messages']['retry_attempts'],'terminal':d['messages'].get('terminal')})
summary={'artifact_id':integrity['artifact_id'],'source':checkout,'local_step':local,'window':final['window'],'final_verdict':final['verdict'],'messages':final['messages'],'correlation':final['correlation'],'queues':final['queues'],'sessions':final['sessions'],'harness':final['harness'],'product_queue_rows':queues,'retry_scope':{'qualification_retry_attempts':qual['messages']['retry_attempts'],'final_retry_attempts':final['messages']['retry_attempts'],'qualification_transport_rejected':qual['queues']['transport_rejected'],'final_transport_rejected':final['queues']['transport_rejected'],'first_nonzero_retry_cut':coordinator[0] if coordinator else None,'limit':'Counter observations only; no inference about previous generations.'},'warning_scan':scan,'checksums_verified_selected_members':verified,'public_copied_members':public,'cleanup_limit':'No independent post-shell OS PID inventory in smoke artifact; zero sessions/queues are not runner-wide zero-process proof.','archive_limit':f"{integrity['zip_bytes']}-byte ZIP range-read only; selected CRC/SHA checked, whole-ZIP digest API-declared only."}
write('correctness-summary.json',summary)
write('correctness-read-command.json',{'command':['python3','-B','performance-private/range-zip.py','performance/correctness-selected-paths.json'],'exit_code':0,'stderr_sha256':sha((private/'range-first.stderr').read_bytes()),'stderr_bytes':(private/'range-first.stderr').stat().st_size,'full_zip_downloaded':False,'downloaded_range_bytes':integrity['downloaded_range_bytes'],'members':len(integrity['selected_members'])})
print(json.dumps({'windows':[(r['seam'],[x['scheduled_to_completion_p99_ms'] for x in r['windows']]) for r in records],'correctness_sent':local['sent'],'rate':local['actual_rate_per_second'],'sampled':final['correlation'],'retry':summary['retry_scope'],'flight':{k:flight.get(k) for k in ('status','trace_bytes','trace_complete')},'public_members':len(public),'range_bytes':integrity['downloaded_range_bytes']},indent=2))
