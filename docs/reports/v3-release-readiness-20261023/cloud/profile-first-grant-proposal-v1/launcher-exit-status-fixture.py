import pathlib,re,subprocess,sys
base=pathlib.Path(__file__).resolve().parent
original=(base.parent/'full-original-distribution-v2/launcher.sh').read_text()
proposed=(base/'launcher.proposed.sh').read_text()
wait=original[original.index('wait_child_uninterrupted() {'):original.index('\nstop_recorded_processes() {')]
start=proposed.index('wait_child_uninterrupted "$COORDINATOR_PID"',proposed.index('record_pid coordinator'))
end=proposed.index('\nif [[ "$WAIT_CHILD_STATUS" -eq 7 ]]',start)
glue=proposed[start:end]
for code in (0,7,8):
 fixture=base/('exit-status-fixture-'+str(code));fixture.mkdir(exist_ok=True)
 script='set -euo pipefail\n'+wait+'\nEVIDENCE_DIR="$1"\n(bash -c "exit '+str(code)+'") &\nCOORDINATOR_PID=$!\n(bash -c "exit 0") &\nPROFILE_DRIVER_PID=$!\n'+glue+'\nprintf "%s\\n" "$WAIT_CHILD_STATUS"\nexit "$WAIT_CHILD_STATUS"\n'
 result=subprocess.run(['bash','-c',script,'fake-launcher',str(fixture)],capture_output=True,text=True,timeout=3)
 assert result.returncode==code,(code,result.returncode,result.stderr)
 assert (fixture/'coordinator-exit-status').read_text().strip()==str(code)
 assert (fixture/'profile-driver-exit-status').read_text().strip()=='0'
 assert result.stdout.strip()==str(code)
 print('PASS coordinator_exit='+str(code)+' preserved across driver_exit=0')
