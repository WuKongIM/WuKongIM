from pathlib import Path
import hashlib,importlib.util,json,subprocess
from unittest import mock
R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-rpm-signature-status')
E=Path('/tmp/wukongim-v3-readiness-20261023/packages/rpm-signature-status-fix-v1')
D=Path('/tmp/wukongim-v3-readiness-20261021/packages/m2b-native-delivery-sdk-repair-v2/native-diagnostic/path-pair')
O=Path(__file__).parent
freeze=json.loads((E/'source-freeze.json').read_bytes());before={r['path']:hashlib.sha256((R/r['path']).read_bytes()).hexdigest() for r in freeze['files']}
assert all(before[r['path']]==r['sha256'] for r in freeze['files'])
s=importlib.util.spec_from_file_location('reviewed_verifier',R/'scripts/verify-production-package-site.py');v=importlib.util.module_from_spec(s);s.loader.exec_module(v)
original=[json.loads(line) for line in (D/'rpm-observations.jsonl').read_text().splitlines()]
rows=[r for r in original if Path(r['argv'][0]).name=='rpmkeys']
candidate=json.loads((D/'result.json').read_bytes())['candidate'];cases=[]
def check(name,path,stdout,stderr,returncode,wanted,signer=candidate):
 commands=[]
 returns=[subprocess.CompletedProcess([],0,b'',b''),subprocess.CompletedProcess([],0,b'',b''),subprocess.CompletedProcess([],returncode,stdout,stderr)]
 def run(command,label):commands.append(command);return returns.pop(0)
 with mock.patch.object(v,'_tool',side_effect=lambda name:name),mock.patch.object(v,'run_command',side_effect=run):got=v.rpm_candidate_signature_verifies(Path(path),b'TEST ONLY PUBLIC CERT',signer)
 assert got==wanted,(name,got,wanted)
 cases.append({'case':name,'expected':wanted,'actual':got,'passed':True,'source_stdout_sha256':hashlib.sha256(stdout).hexdigest(),'source_stderr_sha256':hashlib.sha256(stderr).hexdigest(),'command_calls':len(commands),'no_real_native_tool_calls':True})
for row in rows:
 check('original-'+Path(row['argv'][-1]).parent.name,row['argv'][-1],row['stdout'].encode(),row['stderr'].encode(),row['exit_code'],True)
good=rows[0];path=good['argv'][-1];raw=good['stdout'].encode();header=path.encode()+b':\n';status=raw[len(header):]
for token in (b'NOKEY',b'NOT OK',b'BAD'):
 diagnostic=b'    Payload SHA256 digest: '+token+b'\n'
 for stream in ('stdout','stderr'):check(token.decode()+'-'+stream,path,raw+(diagnostic if stream=='stdout' else b''),diagnostic if stream=='stderr' else b'',0,False)
check('nonzero-with-valid-line',path,raw,b'',1,False)
check('wrong-header',path,b'other.rpm:\n'+status,b'',0,False)
check('unsigned-signature-path','signature.rpm',b'signature.rpm:\n    Payload SHA256 digest: OK\n',b'',0,False)
check('unsupported-SHA1',path,raw.replace(b'RSA/SHA256',b'RSA/SHA1'),b'',0,False)
check('duplicate-signature',path,raw+status.splitlines()[0]+b'\n',b'',0,False)
check('wrong-candidate',path,raw,b'',0,False,'A'*40)
check('key-id-bad-hex',path,raw.replace(candidate[-8:].lower().encode(),b'5bad1234'),b'',0,True,'A'*32+'5BAD1234')
after={r['path']:hashlib.sha256((R/r['path']).read_bytes()).hexdigest() for r in freeze['files']};assert before==after
result={'scope':'Parser-only bounded replay through actual frozen function, with run_command replaced by sealed prior outputs / explicitly synthetic mutations. It does not prove cryptographic success; author native eight-case result is separate.','cases':cases,'source_hashes_before':before,'source_hashes_after':after,'source_unchanged':True,'no_real_native_tool_calls':True,'no_network_calls':True}
(O/'parser-replay.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({'cases':len(cases),'passed':True,'source_unchanged':True}))
