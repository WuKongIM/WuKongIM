#!/usr/bin/env python3
import pathlib,subprocess,os,json,hashlib,datetime,re
R=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud/local-binary-provenance-audit-v1');repo=pathlib.Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM');W=repo/'.worktrees/v3-rc-loss-diagnosis';G=pathlib.Path('/Users/tt/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.11.darwin-arm64');GO=G/'bin/go';C=R.parent
inputs=json.loads((R/'input-hashes.json').read_text());sha=lambda b:hashlib.sha256(b).hexdigest()
def bind(p):
 b=p.read_bytes();inputs[str(p)]={'sha256':sha(b),'bytes':len(b)};return b
def call(args,cwd=W,env=None):
 p=subprocess.run(args,cwd=cwd,env=env or dict(os.environ,GOTOOLCHAIN='local',GOWORK='off',GOPROXY='off',GOSUMDB='off'),capture_output=True,timeout=30);assert p.returncode==0,(args,p.stderr);assert len(p.stdout)<=2<<20;return p.stdout
# No build, download, running target, profile or pressure workload is invoked.
raw=call([str(GO),'list','-mod=readonly','-json','./cmd/wkcli','./cmd/wukongim']);(R/'go-list-worktree-raw.jsonl').write_bytes(raw)
d=json.JSONDecoder();s=raw.decode();pkgs=[]
while s.strip():
 x,n=d.raw_decode(s.lstrip());s=s.lstrip()[n:];pkgs.append({k:x[k] for k in ['Dir','ImportPath','Name','Root','Module','GoFiles','CgoFiles','IgnoredGoFiles','Stale','StaleReason','Error'] if k in x})
assert len(pkgs)==2 and all(x['Dir'].startswith(str(W)+'/cmd/') and x['Module']['Dir']==str(W) and x['Module']['Main'] for x in pkgs)
(R/'go-list-worktree-selected.json').write_text(json.dumps({'command':[str(GO),'list','-mod=readonly','-json','./cmd/wkcli','./cmd/wukongim'],'cwd':str(W),'environment_overrides':{'GOTOOLCHAIN':'local','GOWORK':'off','GOPROXY':'off','GOSUMDB':'off'},'packages':pkgs,'limits':'Current readonly package resolution, not a historical build command or proof of every historical source byte.'},indent=2)+'\n')
for binary,name in [(C/'local-loss-reproduction-v1/bin/wukongim','server-main.objdump.txt'),(C/'full-original-distribution-v2/bin/wkcli','cli-main.objdump.txt')]:
 b=call([str(GO),'tool','objdump','-s','^main\\.main$',str(binary)]);assert b.startswith(b'TEXT main.main(SB) '+str(W).encode());(R/name).write_bytes(b)
# Compare source text from exact revisions with the already-retained compiled witnesses.
sources={};A='a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83';B='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06'
for name in ['pkg/channel/replication/recovery_barrier.go','cmd/wkcli/internal/benchmark/formal_chain_command.go']:
 sources[name]={}
 for rev in [A,B]:
  b=call(['git','show',rev+':'+name],repo);sources[name][rev]={'bytes':len(b),'sha256':sha(b)}
  if name.startswith('pkg/'):
   text=b.decode();sources[name][rev]['has_specific_errors_join_recovery_branch']='return recoveryBarrierResult{}, errors.Join(ch.ErrNotReady, err)' in text
  else:sources[name][rev]['has_reserve_preserving_range_condition']='limit > hardLimit-operationalStop && limit <= hardLimit && stop == limit-(hardLimit-operationalStop)' in b.decode()
  dst=R/'source-snippets'/(rev[:8]+'-'+name.replace('/','_')+'.txt');dst.parent.mkdir(exist_ok=True)
  lines=b.decode().splitlines();lo,hi=(48,77) if name.startswith('pkg/') else (220,240);dst.write_text('\n'.join(f'{i+1}: {l}' for i,l in enumerate(lines) if lo<=i+1<=hi)+'\n')
server=(R/'server-recovery-barrier.objdump.txt').read_text();cli=(R/'cli-formal-runtime-envelope.objdump.txt').read_text();assert str(W)+'/pkg/channel/replication/recovery_barrier.go' in server and 'recovery_barrier.go:67' in server and 'join.go:' in server
assert str(W)+'/cmd/wkcli/internal/benchmark/formal_chain_command.go' in cli and 'formal_chain_command.go:230' in cli and 'SUB R9, R8, R9' in cli
# Freeze Go's own exact VCS discovery source and a filesystem-only reconstruction of its Git root search.
vcs=G/'src/cmd/go/internal/vcs/vcs.go';load=G/'src/cmd/go/internal/load/pkg.go';v=bind(vcs).decode().splitlines();l=bind(load).decode().splitlines();ranges={'vcs.go':[(212,221),(232,264),(578,642)],'pkg.go':[(2496,2587)]}
for name,lines in [('vcs.go',v),('pkg.go',l)]:
 (R/'source-snippets'/('go1.25.11-'+name+'.txt')).write_text('\n'.join(f'{i+1}: {s}' for i,s in enumerate(lines) if any(lo<=i+1<=hi for lo,hi in ranges[name]))+'\n')
search=[];p=W;selected=None
while True:
 q=p/'.git';entry={'directory':str(p),'dot_git_exists':q.exists(),'dot_git_is_directory':q.is_dir(),'dot_git_is_regular_file':q.is_file()};search.append(entry)
 if q.is_dir() and selected is None:selected=p
 if p.parent==p:break
 p=p.parent
assert selected==repo
root_rev=call(['git','rev-parse','HEAD'],selected).decode().strip();work_rev=call(['git','rev-parse','HEAD'],W).decode().strip();assert root_rev==A and work_rev==B
compiler=G/'pkg/tool/darwin_arm64/compile';compiler_version=call([str(compiler),'-V=full']).decode().strip();bind(compiler);bind(GO)
witness={'sources':sources,'go_vcs_root_mechanism':{'go_version':'go1.25.11','git_root_definition':'RootNames includes only .git with isDir=true at vcs.go:218','is_vcs_root_constraint':'os.Stat(...).IsDir() must equal root.isDir at vcs.go:636-640','search_algorithm':'FromDir selects the first recognized VCS root while scanning parents; a .git ordinary file does not match. Nested Git directories remain allowed.','build_info_wiring':'load/pkg.go:2518/2545/2557 resolves cwd/package/module via FromDir; 2570 asks gitStatus on selected repoDir. gitStatus runs status --porcelain and log -1 there.','filesystem_reconstruction':search,'selected_repo_dir':str(selected),'selected_repo_head_now':root_rev,'real_worktree_head_now':work_rev,'claim':'This source-derived filesystem reconstruction explains how an actual worktree build can acquire the outer repository a804/clean VCS stamp. It is not proof of compiling every historical source byte.'},'binary_witnesses':{'server':{'sha256':'c2531b920ba7b9ff99d777c6bf5d988840426a5dc5f15d66ae0005aa2660b379','main_path_worktree':True,'changed_function':'pkg/channel/replication.writeCurrentTermBarrier','compiled_recovery_line_67_and_inlined_errors_join':True,'interpretation':'Positive executable evidence of the 79a recovery-barrier change, which is absent from a804. This refutes a blanket claim that the server is the old a804 product. It does not replace a complete initial build receipt.'},'cli':{'sha256':'e36e5151f4424b0485c1f611d2982f76e6dd0ed3b805c478c70a3bbfdb74ba62','main_path_worktree':True,'changed_function':'cmd/wkcli/internal/benchmark.loadFormalRuntimeEnvelope','compiled_range_subtraction_then_stop_equality_at_line_230':True,'interpretation':'Positive executable evidence consistent with the 79a reserve-preserving range condition, not the a804 two fixed equalities; together with recorded worktree build commands this supports the 79a CLI source association.'}},'installed_toolchain':{'go_absolute_path':str(GO),'compiler_absolute_path':str(compiler),'compiler_version':compiler_version},'no_build_no_target_execution_no_network':True}
(R/'source-witnesses-and-stamp-mechanism.json').write_text(json.dumps(witness,indent=2)+'\n')
for name in ['test/e2e/suite/binary.go','test/e2e/message/online_loss_reproduction/online_loss_reproduction_test.go','AGENTS.md','go.mod','go.sum','pkg/channel/FLOW.md','internal/usecase/conversation/FLOW.md']:
 p=W/name
 if p.is_file():bind(p)
for n in ['local-loss-reproduction-v1','local-loss-reproduction-v3','local-loss-reproduction-v4']:
 p=C/n/'report.json';b=bind(p);x=json.loads(b);assert x['binary_sha256']=='c2531b920ba7b9ff99d777c6bf5d988840426a5dc5f15d66ae0005aa2660b379'
(R/'input-hashes.json').write_text(json.dumps(inputs,indent=2)+'\n')
print(json.dumps({'go_list_packages':[{'path':x['ImportPath'],'dir':x['Dir'],'module_dir':x['Module']['Dir']} for x in pkgs],'go_vcs_reconstruction_selected':str(selected),'outer_stamp':root_rev,'worktree_head':work_rev,'server_has_post_a804_recovery_code':True,'cli_has_post_a804_budget_code':True,'input_hash_count':len(inputs),'compiler_version':compiler_version}))
