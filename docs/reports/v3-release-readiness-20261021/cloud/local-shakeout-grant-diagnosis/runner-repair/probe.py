#!/usr/bin/env python3
"""Bounded integration probe for the actual outer-runner lifecycle, no product I/O."""
import ast
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time

SOURCE_ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-shakeout-grant-repair')

def alive(pid):
    try:
        p = subprocess.run(['ps', '-p', str(pid), '-o', 'stat='], capture_output=True, text=True, timeout=3)
        return p.returncode == 0 and p.stdout.strip() and not p.stdout.lstrip().startswith('Z')
    except ProcessLookupError:
        return False

def worker(runner, directory, case):
    out = Path(directory); run = out/'run'; run.mkdir()
    tree = ast.parse(Path(runner).read_text(), filename=str(runner))
    boundary = next(i for i,n in enumerate(tree.body) if isinstance(n,ast.With) and 'stdout-stderr.log' in ast.unparse(n.items[0].context_expr))
    definitions = [n for n in tree.body[:boundary] if isinstance(n,(ast.Import,ast.ImportFrom,ast.FunctionDef,ast.ClassDef))]
    ns={'__name__':'probe_lifecycle', '__file__':str(runner)}
    exec(compile(ast.Module(body=definitions,type_ignores=[]),str(runner),'exec'),ns)
    class ShortBounds(ast.NodeTransformer):
        def visit_Constant(self,node):
            if isinstance(node.value,int) and not isinstance(node.value,bool):
                if node.value==3000:return ast.copy_location(ast.Constant(1.0 if case=='timeout' else 10.0),node)
                if node.value==120:return ast.copy_location(ast.Constant(.2),node)
                if node.value==20:return ast.copy_location(ast.Constant(.5),node)
                if node.value==15:return ast.copy_location(ast.Constant(1.0),node)
            return node
    tail=ShortBounds().visit(ast.Module(body=tree.body[boundary:],type_ignores=[]));ast.fix_missing_locations(tail)
    child=out/'fake_child.py'
    child.write_text('''import json,os,signal,subprocess,sys,time\nfrom pathlib import Path\no=Path(sys.argv[1]);mode=sys.argv[2]\n(o/'report').mkdir(exist_ok=True)\n(o/'report'/'final.json').write_text(json.dumps({'final':True,'verdict':{'outcome':'operator_stop' if mode=='planned130' else 'passed'}}))\n(o/'helper.pid').write_text(str(os.getpid()))\nif mode in ['cancel','exception','timeout']:\n p=subprocess.Popen([sys.executable,'-c',"import os,signal,time;from pathlib import Path;os.setpgid(0,0);signal.signal(signal.SIGTERM,signal.SIG_IGN);Path("+repr(str(o/'node.pid'))+").write_text(str(os.getpid()));time.sleep(60)"])\n signal.signal(signal.SIGTERM,lambda *_:sys.exit(130))\n (o/'ready').write_text('ready')\n time.sleep(60)\nelse:\n time.sleep(.1)\n sys.exit(8 if mode=='exit8' else 130 if mode=='planned130' else 0)\n''')
    def cmd(argv,env=None):
        return 'fake-source' if argv[1:]==['rev-parse','HEAD'] else ''
    stop=threading.Event(); owned={};errors=[]
    def observe():
        if case=='collector':raise RuntimeError('injected collector failure')
        while not stop.wait(.005):
            for path in run.glob('*.pid'):
                pid=path.read_text().strip()
                if pid not in owned:
                    p=subprocess.run(['ps','-p',pid,'-o','pid=,lstart=,comm='],capture_output=True,text=True)
                    if p.returncode==0:owned[pid]={'name':path.stem,'pid':int(pid),'ps_identity':p.stdout.strip()}
    ns.update(ROOT=SOURCE_ROOT,OUT=out,RUN=run,SOURCE='fake-source',files=[],freeze={},cmd=cmd,
              arguments=[sys.executable,str(child),str(run),case],env=dict(os.environ),
              observer_stop=stop,observations=out/'observations',owned=owned,observer_errors=errors,
              api_token='probe-api-secret',worker_token='probe-worker-secret',observe=observe,
              receipt={'command':[str(child),case],'source_sha':'fake-source','helper_pid':None,'exit_code':None})
    (out/'observations').mkdir()
    ns['observer']=threading.Thread(target=ns['observed_capture'],daemon=True)
    original_write=ns['write'];injected=False
    def write(name,data):
        nonlocal injected
        original_write(name,data)
        if case=='exception' and name=='command.json' and data.get('helper_pid') and not injected:
            injected=True
            # Ensure the independent process group exists before raising at the real post-spawn receipt seam.
            end=time.monotonic()+1
            while not (run/'node.pid').exists() and time.monotonic()<end:time.sleep(.005)
            raise RuntimeError('injected post-spawn receipt failure')
    ns['write']=write
    exec(compile(tail,str(runner),'exec'),ns)

def main():
    if sys.argv[1]=='--worker':
        worker(*sys.argv[2:]);return
    runner=Path(sys.argv[1]);out=Path(sys.argv[2]);out.mkdir(exist_ok=False)
    sentinel=subprocess.Popen([sys.executable,'-c','import time;time.sleep(90)'],start_new_session=True)
    results=[]
    try:
        for case in ['exit0','exit8','planned130','cancel','exception','collector','timeout']:
            d=out/case;d.mkdir();started=time.monotonic()
            with (d/'probe.log').open('wb') as log:
                p=subprocess.Popen([sys.executable,__file__,'--worker',str(runner),str(d),case],stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
                if case=='cancel':
                    end=time.monotonic()+4
                    while not (d/'run'/'node.pid').exists() and p.poll() is None and time.monotonic()<end:time.sleep(.01)
                    p.send_signal(signal.SIGTERM)
                try:rc=p.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    os.killpg(p.pid,signal.SIGKILL);rc=p.wait();rc='probe_timeout'
            pids=[]
            for f in (d/'run').glob('*.pid'):
                try:pids.append(int(f.read_text()))
                except ValueError:pass
            command=d/'command.json'
            if command.exists():
                helper=json.loads(command.read_text()).get('helper_pid')
                if helper:pids.append(helper)
            remaining=sorted(pid for pid in set(pids) if alive(pid))
            cleanup_path=d/'cleanup-verification.json'
            cleanup=json.loads(cleanup_path.read_text()) if cleanup_path.exists() else None
            expected=0 if case=='exit0' else 8 if case=='exit8' else 130 if case=='planned130' else 143 if case=='cancel' else 124 if case=='timeout' else 1
            sentinel_alive=bool(alive(sentinel.pid))
            result={'case':case,'exit_code':rc,'expected_exit_code':expected,'remaining_owned_before_probe_cleanup':remaining,'cleanup_receipt_present':cleanup is not None,'cleanup_pass':cleanup.get('pass') if cleanup else None,'external_sentinel_alive':sentinel_alive,'duration_seconds':round(time.monotonic()-started,3)}
            result['pass']=rc==expected and not remaining and cleanup is not None and cleanup.get('pass') is True and sentinel_alive
            results.append(result)
            # Test supervisor reclaims only exact fake PIDs it recorded, including first-red leaks.
            for pid in remaining:
                try:os.kill(pid,signal.SIGKILL)
                except ProcessLookupError:pass
    finally:
        sentinel.terminate();sentinel.wait(timeout=3)
    receipt={'runner':str(runner),'runner_sha256':hashlib.sha256(runner.read_bytes()).hexdigest(),'probe_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'results':results,'passed':all(r['pass'] for r in results)}
    (out/'summary.json').write_text(json.dumps(receipt,indent=2)+'\n')
    print(json.dumps(receipt,indent=2))
    sys.exit(0 if receipt['passed'] else 1)

if __name__=='__main__':main()
