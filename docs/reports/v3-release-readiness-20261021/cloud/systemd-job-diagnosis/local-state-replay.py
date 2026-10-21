"""Read-only replay of public systemctl fields; never contacts a live host."""
from pathlib import Path
import hashlib, json, subprocess, datetime
root=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job')
out=Path(__file__).resolve().parent
shells=['/bin/bash','/tmp/wukongim-v3-readiness-20261021/scripts-signal-analysis-diagnosis/bash-5.2.21/bash']
env={'PATH':'/usr/bin:/bin:/usr/sbin:/sbin','LC_ALL':'C','LANG':'C'}
classifier=root/'scripts/chat-lifecycle/classify-stage-service-state.sh'
helper=root/'scripts/chat-lifecycle/pre-clock-diagnostic.py'
python='/opt/homebrew/bin/python3'
cases={
 'observed-inactive-empty':'ActiveState=inactive\nJob=\n',
 'active-empty':'ActiveState=active\nJob=\n',
 'inactive-zero':'ActiveState=inactive\nJob=0\n',
 'inactive-queued':'ActiveState=inactive\nJob=8841\n',
 'invalid-job':'ActiveState=active\nJob=not-a-job\n',
 'missing-job':'ActiveState=active\n',
 'duplicate-active':'ActiveState=active\nActiveState=active\n',
 'duplicate-job-empty':'ActiveState=inactive\nJob=\nJob=0\n',
 'overflow-job':'ActiveState=inactive\nJob=4294967296\n',
 'max-job':'ActiveState=inactive\nJob=4294967295\n',
 'zero-padded-job':'ActiveState=inactive\nJob=00\n',
 'unterminated-job':'ActiveState=inactive\nJob=0',
 'trailing-unterminated-garbage':'ActiveState=active\nJob=0\nUnexpected=x',
 'diagnostic-only-known-state':'ActiveState=maintenance\nJob=0\n',
}
rows=[]
for name, raw in cases.items():
 outputs=[]
 for shell in shells:
  r=subprocess.run([shell,str(classifier)],input=raw.encode(),capture_output=True,timeout=3,env=env)
  outputs.append({'shell':shell,'exit_code':r.returncode,'stdout':r.stdout.decode(),'stderr':r.stderr.decode()})
 # The reducer expects all seven properties, unlike the control classifier's two.
 complete='LoadState=loaded\nSubState=dead\nResult=success\nExecMainCode=0\nExecMainStatus=0\n'+raw
 r=subprocess.run([python,'-B',str(helper),'state'],input=complete.encode(),capture_output=True,timeout=3,env=env)
 rows.append({'case':name,'input':raw,'input_sha256':hashlib.sha256(raw.encode()).hexdigest(),'classifier':outputs,'diagnostic_exit':r.returncode,'diagnostic':json.loads(r.stdout)})
guards=[]
for shell in shells:
 for name, command in [('bare-combined', 'set -e; active=x; job=""; [[ -n "$active" && "$job" =~ ^[0-9]+$ ]]; printf survived'),('explicit-combined','set -e; active=x; job=""; [[ -n "$active" && "$job" =~ ^[0-9]+$ ]] || exit 2; printf survived'),('bare-single','set -e; job=""; [[ "$job" =~ ^[0-9]+$ ]]; printf survived')]:
  r=subprocess.run([shell,'-c',command],capture_output=True,timeout=3,env=env)
  guards.append({'shell':shell,'case':name,'command':command,'exit_code':r.returncode,'stdout':r.stdout.decode(),'stderr':r.stderr.decode()})
result={'at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'source_sha':'e30299fa59ea6669ea71024eabe209840f241f19','classifier_sha256':hashlib.sha256(classifier.read_bytes()).hexdigest(),'reducer_sha256':hashlib.sha256(helper.read_bytes()).hexdigest(),'shell_versions':{s:subprocess.check_output([s,'--version'],env=env).decode().splitlines()[0] for s in shells},'cases':rows,'guard_probes':guards,'limits':'Local public-input CLI replay only; no systemctl/SSH/cloud/Actions/package build. Observed host capture was post-kill; no claim of original pre-kill linkage.'}
(out/'local-state-replay.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps([{'case':r['case'],'shell_results':[(x['exit_code'],x['stdout'].strip()) for x in r['classifier']],'diagnostic_valid':r['diagnostic']['valid'],'diagnostic_job':r['diagnostic']['properties'].get('Job')} for r in rows],indent=2))
print(json.dumps(guards,indent=2))
