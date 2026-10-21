import json,os,signal,subprocess,sys,time
from pathlib import Path
name,limit,*command=sys.argv[1:];limit=int(limit)
e=Path(__file__).parent;worktree=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-route-compiler')
start=time.time();record={'command':command,'cwd':str(worktree),'timeout_seconds':limit,'started_at_unix':start}
with (e/(name+'.log')).open('wb') as log:
    process=subprocess.Popen(command,cwd=worktree,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
    try:process.wait(timeout=limit);record['timed_out']=False
    except subprocess.TimeoutExpired:
        os.killpg(process.pid,signal.SIGTERM)
        try:process.wait(timeout=5)
        except subprocess.TimeoutExpired:os.killpg(process.pid,signal.SIGKILL);process.wait()
        record['timed_out']=True
record.update(exit_code=process.returncode,terminal=True,elapsed_seconds=round(time.time()-start,3))
(e/(name+'.json')).write_text(json.dumps(record,indent=2)+'\n')
print(json.dumps(record));print((e/(name+'.log')).read_text()[-1300:])
sys.exit(process.returncode)
