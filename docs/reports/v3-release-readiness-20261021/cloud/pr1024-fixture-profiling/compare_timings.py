from pathlib import Path
import json,datetime,collections
BASE=Path('/tmp/wukongim-v3-readiness-20261021/cloud');D=BASE/'pr1024-fixture-profiling'
sources={
'linux_pr1022_gen1':BASE/'pr1022-ci-generation-1/artifacts-public/12357946122/members/stdout-stderr.log',
'linux_pr1022_gen2':BASE/'pr1022-ci-generation-2/artifacts-public/12360127874/members/stdout-stderr.log',
'linux_pr1024_gen1':BASE/'pr1024-ci-generation-1/candidate/artifacts-public/12371361340/members/stdout-stderr.log',
'mac_v1':BASE/'rc-new-cee5/terminal-evidence-repair/full-integration.jsonl',
'mac_v2':BASE/'rc-new-cee5/terminal-evidence-repair-v2/full-integration.jsonl'}
def epoch(s):return datetime.datetime.fromisoformat(s.replace('Z','+00:00')).timestamp()
result={}
for name,path in sources.items():
 rows=[]
 for line in path.read_text().splitlines():
  if not line.startswith('{'):continue
  try:r=json.loads(line)
  except ValueError:continue
  if 'Action' in r:rows.append(r)
 script=[r for r in rows if r.get('Package')=='github.com/WuKongIM/WuKongIM/scripts'];roots=[r for r in script if r.get('Test') and '/' not in r['Test']];terminal=[r for r in roots if r['Action'] in ('pass','fail','skip')]
 rpath=path.parent/'result.json';outer=json.loads(rpath.read_text()) if rpath.exists() else None
 active={}
 for r in roots:
  if r['Action'] in ('run','cont'):active[r['Test']]=r
  elif r['Action'] in ('pause','pass','fail','skip'):active.pop(r['Test'],None)
 result[name]={'source':str(path),'outer_result':{k:outer.get(k) for k in ['status','exit_code','elapsed_seconds']} if outer else None,'first_event':{k:rows[0].get(k) for k in ['Time','Action','Package']},'first_scripts_event':{k:script[0].get(k) for k in ['Time','Action','Package']},'last_event':{k:rows[-1].get(k) for k in ['Time','Action','Package']},'scripts_observed_seconds':epoch(script[-1]['Time'])-epoch(script[0]['Time']),'root_terminal_counts':dict(collections.Counter(r['Action'] for r in terminal)),'root_terminals':{r['Test']:{'action':r['Action'],'elapsed':r.get('Elapsed')} for r in terminal},'collector_events':[{k:r.get(k) for k in ['Time','Action','Test','Elapsed']} for r in roots if 'TerminalEvidence' in r['Test'] and r['Action']!='output'],'pending_active_roots':list(active)}
for baseline in ['linux_pr1022_gen1','linux_pr1022_gen2','mac_v1']:
 candidate='mac_v2' if baseline=='mac_v1' else 'linux_pr1024_gen1'
 a=result[baseline]['root_terminals'];b=result[candidate]['root_terminals'];diffs=[]
 for t in a.keys()&b.keys():
  if a[t]['action']==b[t]['action']=='pass':diffs.append({'test':t,'before':a[t]['elapsed'],'after':b[t]['elapsed'],'delta':b[t]['elapsed']-a[t]['elapsed']})
 result[candidate]['vs_'+baseline]={'matched_passes':len(diffs),'sum_before':sum(x['before'] for x in diffs),'sum_after':sum(x['after'] for x in diffs),'largest_increases':sorted(diffs,key=lambda x:x['delta'],reverse=True)[:12]}
(D/'timing-comparison.json').write_text(json.dumps(result,indent=2)+'\n')
for name,r in result.items():
 print(name,json.dumps({k:v for k,v in r.items() if k not in ['source','root_terminals','collector_events']},separators=(',',':')))
 print('collector',json.dumps(r['collector_events']))
