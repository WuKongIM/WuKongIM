from pathlib import Path
import json, re, sys, subprocess, shlex

report = Path(sys.argv[1] if len(sys.argv)>1 else '/tmp/mqtt-stage-full-5dfa')
raw = report/'stage-aggregates.log'
if not raw.exists():
    for line in subprocess.check_output(['ps','-Ao','args='],text=True).splitlines():
        try: args=shlex.split(line)
        except ValueError: continue
        if args and args[0]=='/tmp/wukongim-mqtt-stage-v3-5dfa' and '-config' in args:
            raw=Path(args[args.index('-config')+1]).parent/'stderr.log'
            break
records=[]
for line in raw.read_text().splitlines():
    if line.startswith('[DEBUG-mqtt-stage-v3-5dfa] '): records.append(json.loads(line.split(' ',1)[1]))
log = report.with_suffix('.log').read_text()
match=re.search(r'phase fanout started unix_ns=(\d+)',log)
if not match:
    print('Still preparing; diagnostic components present:', sorted(set(r['component'] for r in records)))
    sys.exit(0)
start=int(match[1])
stopmatch=re.search(r'phase fanout completed in \d+ ms unix_ns=(\d+)',log)
stop=int(stopmatch[1]) if stopmatch else max(r['unix_ns'] for r in records)
# Ignore phase edges; each component independently uses the last completed
# aggregate at or before each boundary. Exact component timestamps are recorded.
lo=start+15*10**9
hi=stop-10*10**9
summary={'fanout_started_unix_ns':start,'requested_window':[lo,hi],'components':{}}
for component in sorted(set(r['component'] for r in records)):
    seq=[r for r in records if r['component']==component]
    left=[r for r in seq if r['unix_ns']<=lo]
    right=[r for r in seq if r['unix_ns']<=hi]
    if not left or not right: continue
    a,b=left[-1],right[-1]
    if b['unix_ns']<=a['unix_ns']: continue
    old={(r['stage'],r['kind']):r for r in a['rows']}
    rows=[]
    for row in b['rows']:
        before=old.get((row['stage'],row['kind']),{'calls':0,'ns':0,'hist':[0]*12,'results':[0]*12})
        count=row['calls']-before['calls'];ns=row['ns']-before['ns']
        if count:
            rows.append({'stage':row['stage'],'kind':row['kind'],'calls':count,'total_s':ns/1e9,'mean_ms':ns/count/1e6,'hist':[x-y for x,y in zip(row['hist'],before['hist'])],'results':[x-y for x,y in zip(row['results'],before['results'])]})
    summary['components'][component]={'bounds':[a['unix_ns'],b['unix_ns']],'seconds':(b['unix_ns']-a['unix_ns'])/1e9,'enqueued':b['enqueued']-a['enqueued'],'recovery_empty':b['recovery_empty']-a['recovery_empty'],'rows':rows}
if (report/'stage-aggregates.log').exists():
    (report/'stage-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
for component,c in summary['components'].items():
    print(component, 'window_s',round(c['seconds'],3),'enqueued',c['enqueued'],'recovery_empty',c['recovery_empty'])
    for row in c['rows']:
        if component!='node' or row['stage']!='unattributed':
            print(' ',row['stage'],row['kind'],'calls',row['calls'],'s',round(row['total_s'],2),'mean_ms',round(row['mean_ms'],2))
