from pathlib import Path
import ast,difflib,hashlib,json,re,shlex,subprocess
p=Path(__file__).absolute().parent;a=p.parent/'full-original-distribution-v2';old=p.parent/'profile-first-grant-proposal-v1';out=p.parent/'full-original-distribution-exact79a-profile-v1';src=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-exact-source-79a');build=p.parent/'exact-source-build-79a-cloud-flags-v1'
assert not out.exists();out.mkdir(mode=0o700)
sha=lambda f:hashlib.sha256(f.read_bytes()).hexdigest()
server='5b30743481398ad8175476d18ffbc676c6da5288657919ae8e704324e67351ab';cli='081948b0b9e70f336d50fbfdb0d2943854ee69e42c7fd416d43534e82b46bdf5';helper=sha(p/'capture-first-grant-profiles.sh')
original=(a/'launcher.sh').read_text();s=original.replace("ROOT_DIR='/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis'","ROOT_DIR="+shlex.quote(str(src))).replace(str(a),str(out))
s=s.replace('WUKONGIM_BIN="$RUN_DIR/bin/wukongim"','WUKONGIM_BIN='+shlex.quote(str(build/'darwin-arm64/wukongim'))).replace('WK_CLI_BIN="$RUN_DIR/bin/wkcli"','WK_CLI_BIN='+shlex.quote(str(build/'darwin-arm64/wkcli')))
x=json.loads((a/'report/run-start.json').read_text())
cmd=['python3',str(p/'watch-first-grant.py'),'--run-dir',str(out),'--out-dir',str(out/'profiles-node2'),'--source-root',str(src),'--source-revision','79a7177032a6ae1ec7015cefe0e6f94eb0f19f06','--binary',str(build/'darwin-arm64/wukongim'),'--binary-sha256',server,'--helper',str(p/'capture-first-grant-profiles.sh'),'--helper-sha256',helper,'--base-port','25000','--run-hash',x['run_hash'],'--assignment-hash',x['assignment_hash'],'--generation','1']
hook='\n# One optional diagnostic in the same owned workload process group.\n'+shlex.join(cmd)+' --owner-pgid "$$" --deadline-mono "$LOCAL_REPRO_DEADLINE_MONO" >"$LOG_DIR/profile-driver.log" 2>&1 &\nPROFILE_DRIVER_PID=$!\nrecord_pid profile-driver "$PROFILE_DRIVER_PID"\n'
s=s.replace('record_pid coordinator "$COORDINATOR_PID"\n','record_pid coordinator "$COORDINATOR_PID"\n'+hook)
oldglue='wait_child_uninterrupted "$COORDINATOR_PID"\nprintf \'%s\\n\' "$WAIT_CHILD_STATUS" >"$EVIDENCE_DIR/coordinator-exit-status"\n'
newglue='wait_child_uninterrupted "$COORDINATOR_PID"\nCOORDINATOR_EXIT_STATUS="$WAIT_CHILD_STATUS"\nprintf \'%s\\n\' "$COORDINATOR_EXIT_STATUS" >"$EVIDENCE_DIR/coordinator-exit-status"\nwait_child_uninterrupted "$PROFILE_DRIVER_PID"\nprintf \'%s\\n\' "$WAIT_CHILD_STATUS" >"$EVIDENCE_DIR/profile-driver-exit-status"\nWAIT_CHILD_STATUS="$COORDINATOR_EXIT_STATUS"\n'
assert oldglue in s;s=s.replace(oldglue,newglue)
(out/'launcher.sh').write_text(s);(p/'launcher.proposed.sh').write_text(s)
(p/'launcher-vs-A.diff').write_text(''.join(difflib.unified_diff(original.splitlines(True),s.splitlines(True),fromfile='A/launcher.sh',tofile='exact79a/launcher.sh')))
original=(a/'supervise.py').read_text();s=original.replace('import urllib.request\n','import urllib.request\nfrom exact_build_guard import verify_exact_build, PRODUCT_BINARIES\n').replace(str(a.parent.parent/'NO_SUCH_PATH'),'NO_CHANGE')
s=s.replace('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis',str(src))
start=s.index('    assert subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip() == SOURCE')
end=s.index('    assert not any((OUT / name).exists()',start)
s=s[:start]+'    exact_build = verify_exact_build()\n'+s[end:]
s=s.replace('    (OUT / "bin").mkdir(exist_ok=True)\n','')
start=s.index('    source_binary = OUT.parent / "local-loss-reproduction-v1/bin/wukongim"');end=s.index('    assert time.monotonic() - started_mono <= 60',start)
s=s[:start]+s[end:]
s=s.replace('"go_version": capture(["go", "version"]),','"exact_build": exact_build,')
s=s.replace('hashlib.sha256((OUT / "bin" / name).read_bytes()).hexdigest()','hashlib.sha256(PRODUCT_BINARIES[name].read_bytes()).hexdigest()')
s=s.replace('    reason, stop_sent, stop_at = code, True, time.monotonic()\n','    reason, stop_sent, stop_at = code, True, time.monotonic()\n    (OUT / "evidence/profile-stop").write_text(code + "\\n")\n')
assert 'go", "build' not in s and 'source_binary' not in s and 'local-loss-reproduction-v1/bin' not in s
ast.parse(s);(out/'supervise.py').write_text(s);(p/'supervise.proposed.py').write_text(s)
(p/'supervisor-vs-A.diff').write_text(''.join(difflib.unified_diff(original.splitlines(True),s.splitlines(True),fromfile='A/supervise.py',tofile='exact79a/supervise.py')))
for f in ['original-distribution-local.yaml','watchdog.py','verify-slots.py']:(out/f).write_bytes((a/f).read_bytes())
(out/'exact_build_guard.py').write_bytes((p/'exact_build_guard.py').read_bytes())
for name in ['launcher.sh','supervise.py','exact_build_guard.py']:(out/name).chmod(0o700)
for name in ['capture-first-grant-profiles.sh','launcher.proposed.sh']:subprocess.run(['bash','-n',str(p/name)],check=True)
for name in ['watch-first-grant.py','exact_build_guard.py','supervise.proposed.py']:ast.parse((p/name).read_text())
# Read-only native guard: metadata extraction only, never execute either product.
checked=subprocess.run(['python3',str(out/'exact_build_guard.py')],check=True,capture_output=True,text=True,timeout=10);(p/'static-exact-build-preflight.json').write_text(checked.stdout)
configs={};pattern=re.compile(r'^\s*(channel_store_append_workers|channel_store_apply_workers|channel_rpc_workers|channel_rpc_batch_max_items|gnet_multicore|gnet_num_event_loop|runtime_async_send_workers|runtime_async_send_queue_capacity|recipient_worker_concurrency)\s*=')
for n in [1,2,3]:
 raw=(src/('scripts/wukongim/wukongim-node'+str(n)+'.toml')).read_text();body=''.join(line for line in raw.splitlines(True) if not pattern.match(line));assert body.encode()==(a/('config/node'+str(n)+'.toml')).read_bytes();configs[str(n)]=hashlib.sha256(body.encode()).hexdigest()
receipt={'schema':'wukongim/exact79a-local-profile-proposal/v2','product_started':False,'source_root':str(src),'source_revision':'79a7177032a6ae1ec7015cefe0e6f94eb0f19f06','future_run_dir':str(out),'built_binaries_used_directly_no_copy_no_build':{'wukongim':{'path':str(build/'darwin-arm64/wukongim'),'sha256':server},'wkcli':{'path':str(build/'darwin-arm64/wkcli'),'sha256':cli}},'A_inputs_byte_unchanged':{f:sha(out/f) for f in ['original-distribution-local.yaml','watchdog.py','verify-slots.py']},'materialized_node_config_hashes_equal_A':configs,'launcher_sha256':sha(out/'launcher.sh'),'supervisor_sha256':sha(out/'supervise.py'),'exact_guard_sha256':sha(out/'exact_build_guard.py'),'watcher_sha256':sha(p/'watch-first-grant.py'),'helper_sha256':helper,'limits_unchanged':{'total_seconds':600,'watchdog_TERM_seconds':570,'watchdog_KILL_after_seconds':5,'startup_seconds':360,'target_traffic_seconds':120,'latest_stop_seconds':480,'exit7_hold_seconds':90},'config_unchanged':{'gateway_batch_cap':1,'slot_compaction_overhead':1000,'slot_compaction_interval':'5s','node_GOMAXPROCS':2,'load_GOMAXPROCS':1},'original_strict_cli_loader':'Unchanged bench soak chat-lifecycle LoadConfig/Config.Validate; no standalone deployment validator. CLI execution deferred until actual authorized run.','sampler_change':'only goroutine debug0/.pb.gz and finite per-request status; CPU10s+nominal5sGOR, caps/deadlines and ownership retained','runtime_directories_present':{d:(out/d).exists() for d in ['data','pids','workers','report']}}
assert not any(receipt['runtime_directories_present'].values());(p/'materialization-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt,indent=2))
