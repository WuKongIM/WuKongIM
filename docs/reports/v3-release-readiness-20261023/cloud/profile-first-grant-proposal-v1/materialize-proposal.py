import ast,difflib,hashlib,json,pathlib,shlex,subprocess
p=pathlib.Path(__file__).absolute().parent
a=p.parent/'full-original-distribution-v2'
future=p.parent/'full-original-distribution-profile-node2-v1'
assert not future.exists()
sha=lambda b:hashlib.sha256(b).hexdigest()
original=(a/'launcher.sh').read_text();out=original.replace(str(a),str(future));assert out!=original
source='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06'
preflight=json.loads((a/'preflight.json').read_text())
binarysha=preflight['binary_sha256']['wukongim']
helpersha=sha((p/'capture-first-grant-profiles.sh').read_bytes())
runstart=json.loads((a/'report/run-start.json').read_text())
command=['python3',str(p/'watch-first-grant.py'),'--run-dir',str(future),'--out-dir',str(future/'profiles-node2'),'--source-root','/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis','--source-revision',source,'--binary',str(future/'bin/wukongim'),'--binary-sha256',binarysha,'--helper',str(p/'capture-first-grant-profiles.sh'),'--helper-sha256',helpersha,'--base-port','25000','--run-hash',runstart['run_hash'],'--assignment-hash',runstart['assignment_hash'],'--generation',str(runstart['generation'])]
hook='\n# Temporary node2 diagnostic; shares this exact owned workload process group.\n'+shlex.join(command)+' --owner-pgid "$$" --deadline-mono "$LOCAL_REPRO_DEADLINE_MONO" >"$LOG_DIR/profile-driver.log" 2>&1 &\nPROFILE_DRIVER_PID=$!\nrecord_pid profile-driver "$PROFILE_DRIVER_PID"\n'
needle='record_pid coordinator "$COORDINATOR_PID"\n';assert out.count(needle)==1
out=out.replace(needle,needle+hook)
old='wait_child_uninterrupted "$COORDINATOR_PID"\nprintf \'%s\\n\' "$WAIT_CHILD_STATUS" >"$EVIDENCE_DIR/coordinator-exit-status"\n'
new='wait_child_uninterrupted "$COORDINATOR_PID"\nCOORDINATOR_EXIT_STATUS="$WAIT_CHILD_STATUS"\nprintf \'%s\\n\' "$COORDINATOR_EXIT_STATUS" >"$EVIDENCE_DIR/coordinator-exit-status"\nwait_child_uninterrupted "$PROFILE_DRIVER_PID"\nprintf \'%s\\n\' "$WAIT_CHILD_STATUS" >"$EVIDENCE_DIR/profile-driver-exit-status"\nWAIT_CHILD_STATUS="$COORDINATOR_EXIT_STATUS"\n'
assert out.count(old)==1;out=out.replace(old,new)
(p/'launcher.proposed.sh').write_text(out)
(p/'launcher-adaptation.diff').write_text(''.join(difflib.unified_diff(original.splitlines(True),out.splitlines(True),fromfile='A/launcher.sh',tofile='proposed/launcher.sh')))
original=(a/'supervise.py').read_text()
needle='    reason, stop_sent, stop_at = code, True, time.monotonic()\n'
assert original.count(needle)==1
out=original.replace(needle,needle+'    # Fence this optional diagnostic before coordinator admission is stopped.\n    (OUT / "evidence/profile-stop").write_text(code + "\\n")\n')
ast.parse(out)
(p/'supervise.proposed.py').write_text(out)
(p/'supervisor-adaptation.diff').write_text(''.join(difflib.unified_diff(original.splitlines(True),out.splitlines(True),fromfile='A/supervise.py',tofile='proposed/supervise.py')))
subprocess.run(['bash','-n',str(p/'launcher.proposed.sh')],check=True)
subprocess.run(['bash','-n',str(p/'capture-first-grant-profiles.sh')],check=True)
ast.parse((p/'watch-first-grant.py').read_text())
inputs=['original-distribution-local.yaml','watchdog.py','verify-slots.py']
receipt={'schema':'wukongim/temporary-profile-proposal-static-check/v1','not_started':True,'real_output_directory_exists':future.exists(),'future_output_directory':str(future),'source_revision':source,'original_binary_sha256':binarysha,'helper_sha256':helpersha,'watcher_sha256':sha((p/'watch-first-grant.py').read_bytes()),'a_inputs_to_copy_unchanged':{f:sha((a/f).read_bytes()) for f in inputs},'source_launcher_sha256':sha((a/'launcher.sh').read_bytes()),'source_supervisor_sha256':sha((a/'supervise.py').read_bytes()),'launcher_sha256':sha((p/'launcher.proposed.sh').read_bytes()),'supervisor_sha256':sha((p/'supervise.proposed.py').read_bytes()),'strict_loader_preserved':'existing wkcli bench soak chat-lifecycle --config; original LoadConfig/Config.Validate','run_start_expected':runstart,'static_checks':['bash -n helper','bash -n launcher','Python AST watcher/supervisor'],'unchanged_limits':{'total_seconds':600,'watchdog_term_seconds':570,'watchdog_kill_after_seconds':5,'first_grant_deadline_seconds':360,'traffic_target_seconds':120,'latest_stop_seconds':480,'product_failure_hold_seconds':90},'configuration_changes':[],'profile_requests_only':['node2 CPU seconds=10','node2 goroutine debug=2 nominal shell timer +5s'],'future_materialization_required':'Copy these launcher/supervisor under their original basenames to fresh future directory and copy the three listed A inputs byte for byte; do not execute files from proposal directory.'}
(p/'static-check-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')
(p/'fake-test-results-metadata-final.json').write_bytes((p/'fake-test-results.json').read_bytes())
print(json.dumps({'not_started':True,'helper_sha256':helpersha,'watcher_sha256':receipt['watcher_sha256'],'launcher_sha256':receipt['launcher_sha256'],'supervisor_sha256':receipt['supervisor_sha256']}))
