from pathlib import Path
import hashlib,json,subprocess,os
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-systemd-job')
OUT=Path(__file__).parent
EVIDENCE=Path('/tmp/wukongim-v3-readiness-20261021/cloud/systemd-job-diagnosis')
FROZEN=Path('/tmp/wukongim-v3-readiness-20261021/cloud/rc-continuation-e302/terminal-public-frozen')
sha=lambda b:hashlib.sha256(b).hexdigest()
env=dict(os.environ,LC_ALL='C',LANG='C')
shells={k:json.loads((EVIDENCE/f'green-{k}-default.receipt.json').read_text())['shell'] for k in ['bash3','bash5']}
rows=[]
extra=b'LoadState=loaded\nSubState=running\nResult=success\nExecMainCode=0\nExecMainStatus=0\n'
cases=[]
for state in ['active','activating','reloading','inactive','failed','deactivating']:
 for job in ['', '0','00','0000000000','0000000088','4294967295']:
  want='running' if state in ['active','activating','reloading'] else 'pending' if state=='inactive' and int(job or '0') else 'terminal'
  cases.append((state+':'+repr(job),f'ActiveState={state}\nJob={job}'.encode(),want,int(job or '0')))
for job in ['-0','+0',' 0','0 ','0x10','0.0','4294967296','00000000000','1e2','a','０']:
 cases.append(('invalid:'+repr(job),f'ActiveState=active\nJob={job}\n'.encode(),None,None))
for name,b in [('empty',b''),('missing-job',b'ActiveState=active'),('missing-state',b'Job='),('duplicate-empty',b'Job=\nJob='),('duplicate-state',b'ActiveState=active\nActiveState=active'),('third-line',b'ActiveState=active\nJob=\nX=0'),('blank-line',b'ActiveState=active\nJob=\n\n'),('empty-state',b'ActiveState=\nJob='),('reversed-no-final-newline',b'Job=\nActiveState=active')]:
 cases.append((name,b,'running' if name=='reversed-no-final-newline' else None,0 if name=='reversed-no-final-newline' else None))
for name,b,want,job in cases:
 shell_out={}
 for k,shell in shells.items():
  r=subprocess.run([shell,str(ROOT/'scripts/chat-lifecycle/classify-stage-service-state.sh')],input=b,capture_output=True,env=env,timeout=3)
  ok=r.returncode==(0 if want else 2) and r.stdout==((want+'\n').encode() if want else b'') and r.stderr==b''
  shell_out[k]={'exit':r.returncode,'stdout':r.stdout.decode(),'stderr':r.stderr.decode(),'ok':ok}
 raw=extra+b
 p=subprocess.run(['python3','-B',str(ROOT/'scripts/chat-lifecycle/pre-clock-diagnostic.py'),'state'],input=raw,capture_output=True,env=env,timeout=3)
 d=json.loads(p.stdout)
 diagnostic_ok=p.returncode==0 and d['valid']==bool(want) and d['sha256']==sha(raw) and d['bytes']==len(raw) and (not want or d['properties']['Job']==job)
 rows.append({'name':name,'input_hex':b.hex(),'expected':want,'shells':shell_out,'diagnostic':d,'diagnostic_ok':diagnostic_ok})
raw=(FROZEN/'original-state-safe-reconstruction.txt').read_bytes()
original_files={}
for filename in ['diagnostic.json','unit.json']:
 path=FROZEN/'verified/attempt-2/pre-clock-generation-1'/filename
 author=EVIDENCE/'run33-original'/filename
 original_files[filename]={'path':str(path),'sha256':sha(path.read_bytes()),'same_author_copy':path.read_bytes()==author.read_bytes()}
filtered=subprocess.run(['sed','-E','/^(LoadState|SubState|Result|ExecMainCode|ExecMainStatus)=/d'],input=raw+b'\n',capture_output=True,check=True).stdout
original_replay={}
for key,shell in shells.items():
 results={}
 for version,script in [('before',subprocess.check_output(['git','show','HEAD:scripts/chat-lifecycle/classify-stage-service-state.sh'],cwd=ROOT)),('after',(ROOT/'scripts/chat-lifecycle/classify-stage-service-state.sh').read_bytes())]:
  path=OUT/f'classifier-{version}.sh';path.write_bytes(script)
  r=subprocess.run([shell,str(path)],input=filtered,capture_output=True,env=env,timeout=3)
  results[version]={'exit':r.returncode,'stdout':r.stdout.decode(),'stderr':r.stderr.decode(),'source_sha256':sha(script)}
 original_replay[key]=results
p=subprocess.run(['python3','-B',str(ROOT/'scripts/chat-lifecycle/pre-clock-diagnostic.py'),'state'],input=raw,capture_output=True,env=env,timeout=3)
d=json.loads(p.stdout)
result={'matrix_cases':len(rows),'matrix_failures':[r['name'] for r in rows if not r['diagnostic_ok'] or not all(s['ok'] for s in r['shells'].values())],'shells':{k:{'path':v,'sha256':sha(Path(v).read_bytes()),'version':subprocess.check_output([v,'--version'],env=env,text=True).splitlines()[0]} for k,v in shells.items()},'original':{'bytes':len(raw),'sha256':sha(raw),'filtered_bytes':len(filtered),'filtered_sha256':sha(filtered),'original_files':original_files,'replay':original_replay,'diagnostic':d,'original_hash_preserved':d['sha256']==sha(raw) and d['field_sha256']['Job']==sha(b'')},'rows':rows}
(OUT/'probe-result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:v for k,v in result.items() if k!='rows'},indent=2))
assert not result['matrix_failures']
assert all(x['same_author_copy'] for x in original_files.values())
assert d['valid'] and d['properties']['Job']==0 and result['original']['original_hash_preserved']
