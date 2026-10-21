from pathlib import Path
import json,subprocess,sys,time
p=Path(__file__).resolve().parent
for i in range(2,10):
 time.sleep(75)
 r=subprocess.run([sys.executable,'-B',str(p/'poll-postmerge.py'),f'{i:02d}'],capture_output=True,text=True)
 (p/f'postmerge-{i:02d}-output.json').write_text(r.stdout)
 if r.returncode:
  (p/f'postmerge-{i:02d}-read-error.log').write_text(r.stderr);print(json.dumps({'read_error':True,'poll':i}),flush=True);sys.exit(1)
 s=json.loads(r.stdout);print(json.dumps(s),flush=True)
 if s['runs'] and all(x['status']=='completed' for x in s['runs']):sys.exit(0)
 if any(j['conclusion'] in ['failure','cancelled','timed_out'] for r in s['runs'] for j in r['jobs']):sys.exit(0)
print(json.dumps({'bounded_watch_ended':True}),flush=True)
