import ast, hashlib, json, os, selectors, shlex, signal, subprocess, sys, tempfile, time
from pathlib import Path
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-preview-fixture')
BASE='19ce29fb2233b3039e29c503963e27bd1fab972a'
OUT=Path(__file__).resolve().parent

def source(path,old=False):
    return subprocess.check_output(['git','show',BASE+':'+path],cwd=ROOT).decode() if old else (ROOT/path).read_text()
def extracted(path,names,old=False):
    nodes=[n for n in ast.parse(source(path,old)).body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef)) and n.name in names]
    assert len(nodes)==len(names)
    env=dict(globals(),SCRIPT_DIR=ROOT/'scripts',MAX_LOG=8*1024*1024)
    exec(compile(ast.Module(body=nodes,type_ignores=[]),path,'exec'),env)
    return env
v='scripts/validate-production-package-clients.py'; d='tests/native_stable_clients_drill.py'
old=extracted(v,['_installed_cli_command'],True)['_installed_cli_command']
new=extracted(v,['_installed_cli_command'])['_installed_cli_command']
mod=extracted(d,['_installed_phase_events','run_command']);parse=mod['_installed_phase_events']; run=mod['run_command']
events=[{'phase':p,'event':e} for p in ('checksum','signature','dependencies','cli') for e in ('begin','complete')]
def line(e):return ('WK_INSTALLED_STAGE_V1:'+e['phase']+':'+e['event']+'\n').encode()
valid=b''.join(map(line,events))
cases=[('exact-eight',valid,events,False),('exact-cap',valid*2,events*2,False),('over-cap',valid*2+line(events[0]),events*2,True),('unfinished',line(events[0])[:-1],[],False),('unknown-crlf-whitespace',b'WK_INSTALLED_STAGE_V1:unknown:begin\n '+line(events[0])+line(events[0]).replace(b'\n',b'\r\n'),[],False),('noise-before-and-after',b'x'*9000+b'\n'+valid+b'\xff'*9000+b'\n',events,False),('duplicates',line(events[0])*3,[events[0]]*3,False)]
for name,raw,want,truncated in cases:
    for value in (raw,bytearray(raw)):
        got=parse(value);assert got=={'schema':'wukongim/native-installed-phase-events/v1','events':want,'truncated':truncated},(name,got)
proof={'parser_cases':[x[0] for x in cases],'parser_input_types':['bytes','bytearray'],'parser_assertions':len(cases)*2,'command_equivalence':[]}
with tempfile.TemporaryDirectory(prefix='review-installed-phase-') as tmp:
    root=Path(tmp); package=root/'candidate';package.write_bytes(b'TEST ONLY');cert=root/'public.asc';cert.write_bytes(b'TEST ONLY'); out=root/'output';out.mkdir();tools=root/'tools';tools.mkdir()
    for family in ('apt','rpm'):
        args=('fixed-image',package,out,cert,family,{'version':'3.0.0','commit':'a'*40},'b'*64)
        before=old(*args);after=new(*args)
        assert before[:-1]==after[:-1]
        stripped=''.join(x for x in after[-1].splitlines(keepends=True) if not x.startswith("printf '\\n%s\\n' 'WK_INSTALLED_STAGE_V1:"))
        assert stripped==before[-1]
        proof['command_equivalence'].append({'family':family,'argv_except_script_identical':True,'script_only_literal_markers_added':True,'original_script_sha256':hashlib.sha256(before[-1].encode()).hexdigest()})
    stubs={'sha256sum':'cat >/dev/null\nexit 0','rpm':'exit 0','dnf':"printf '%06000d' 0\nexit 0",'python3':'exit 23'}
    for name,body in stubs.items():
        p=tools/name;p.write_text('#!/bin/sh\n'+body+'\n');p.chmod(0o755)
    script=new('fixed-image',package,out,cert,'rpm',{'version':'3.0.0','commit':'a'*40},'b'*64)[-1].replace('/evidence/installed-cli.json',shlex.quote(str(out/'installed-cli.json')))
    evidence={'commands':[]}
    result=run(evidence,'installed-client',['bash','-euc',script],timeout=5,env={'PATH':str(tools)+os.pathsep+os.defpath,'WK_PACKAGE_SHA256':'b'*64,'WK_EXPECTED_VERSION':'3.0.0','WK_EXPECTED_COMMIT':'a'*40})
    record=evidence['commands'][0]
    assert result.returncode==23 and record['exit_code']==23
    assert record['installed_phase_events']['events']==events[:-1]
    assert not record['installed_phase_events']['truncated']
    assert record['output_size']==len(result.stdout)>6000
    assert record['output_sha256']==hashlib.sha256(result.stdout).hexdigest()
    assert 'checksum:begin' not in record['output_tail']
    assert record['output_tail']==result.stdout[-4096:].decode()
    assert not record['timed_out'] and not record['output_limit']
    proof['actual_generated_shell']={'family':'rpm','fault':'controlled CLI exit 23 after dependency tool emits 6000 bytes without newline','returncode':result.returncode,'output_size':record['output_size'],'output_sha256':record['output_sha256'],'events':record['installed_phase_events'],'early_checksum_absent_from_tail':True,'capture_hash_and_size_preserved':True,'real_native_DNF_or_CLI_executed':False,'all_child_processes_terminal':True}
for path in (v,d):
    a=ast.parse(source(path,True));b=ast.parse(source(path))
    if path==v:
        a.body=[n for n in a.body if not isinstance(n,ast.FunctionDef) or n.name!='_installed_cli_command']
        b.body=[n for n in b.body if not isinstance(n,ast.FunctionDef) or n.name!='_installed_cli_command']
    else:
        b.body=[n for n in b.body if not isinstance(n,ast.FunctionDef) or n.name!='_installed_phase_events']
        f=next(n for n in b.body if isinstance(n,ast.FunctionDef) and n.name=='run_command')
        tail=f.body[-1].finalbody[-1]
        assert isinstance(tail,ast.If) and ast.unparse(tail.test)=="stage == 'installed-client'"
        f.body[-1].finalbody.pop()
    assert ast.dump(a,include_attributes=False)==ast.dump(b,include_attributes=False)
proof['other_validator_and_drill_AST_unchanged']=True
proof['historical_timeout_subphase']='unknown; this controlled fixture is not a reproduction'
(OUT/'probe-result.json').write_text(json.dumps(proof,sort_keys=True,indent=2)+'\n')
print(json.dumps({'passed':True,'pure_parser_assertions':14,'generated_shell_probes':1,'command_equivalence_families':2,'all_child_processes_terminal':True}))
