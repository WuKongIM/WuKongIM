#!/usr/bin/env python3
import argparse
import json
from pathlib import Path
parser=argparse.ArgumentParser(description="Compare retained cgroup cuts without running workload or modifying evidence")
parser.add_argument('--artifact-root',type=Path,required=True)
parser.add_argument('--output',type=Path,required=True,help="New output file; existing files are never overwritten")
args=parser.parse_args()
root=args.artifact_root.resolve()
output=args.output.resolve()
if output.exists():parser.error('--output must be a new file')
if not output.parent.is_dir():parser.error('--output parent directory must exist')
host=json.loads((root/'host.json').read_text())
controls=host['cgroup']['resolved']; names=('cpu.max','cpuset.cpus.effective','cpuset.mems.effective','memory.max','memory.high')
checks=0;failures=[];memberships=set();locations=set()
for directory in sorted((root/'blocks').iterdir()):
 r=json.loads((directory/'report.json').read_text())
 cuts=r['fixed_environment']['window_resource_cuts']
 if len(cuts)!=6:failures.append(directory.name+': cut count')
 for cut in cuts:
  for process in cut['processes']:
   cg=process['cgroup_v2'];locations.add(json.dumps(cg['location'],sort_keys=True))
   for ancestor,expected in controls.items():
    for name in names:
     raw=cg['visible_ancestor_controls'][ancestor][name];actual=raw.get('content', '').strip() if 'content' in raw else None
     checks+=1
     if raw['path']!=ancestor+'/'+name or actual!=expected[name] or (actual is None and not raw.get('error')):failures.append(f'{directory.name}/{cut["name"]}/{process["pid"]}/{ancestor}/{name}')
 for window in r['windows']:
  for point in window['cpu_samples']['points']:
   for process in point['processes']:
    raw=process['cgroup_membership']; memberships.add(raw.get('content'));checks+=1
    if raw.get('content')!=host['cgroup']['membership']:failures.append(f'{directory.name}: sample membership drift')
result={'checks':checks,'pass':not failures,'failures':failures,'observed_memberships':sorted(memberships),'distinct_locations':len(locations),'compared_controls':list(names),'root_missing_is_unknown':True}
with output.open('x') as stream:stream.write(json.dumps(result,indent=2)+'\n')
print(json.dumps(result,indent=2))
