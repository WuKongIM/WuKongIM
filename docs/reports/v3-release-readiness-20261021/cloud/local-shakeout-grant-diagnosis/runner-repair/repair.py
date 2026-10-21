from pathlib import Path
p=Path('/tmp/wukongim-v3-readiness-20261021/local-shakeout-ed705-diagnosis/run-shakeout-once.py')
s=p.read_text().replace('import argparse\n','import argparse\nimport importlib.util\nimport sys\n')
s=s.replace("'scripts/storage-metrics-cut-consistent.awk','configs/wkbench/chat-lifecycle/local-shakeout.yaml',", "'scripts/storage-metrics-cut-consistent.awk','scripts/run-v3-candidate-check.py',\n         'configs/wkbench/chat-lifecycle/local-shakeout.yaml',")
s=s.replace("    (OUT / name).write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\\n')", "    path = OUT / name\n    temporary = path.with_suffix(path.suffix + '.tmp')\n    temporary.write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\\n')\n    temporary.replace(path)")
s=s.replace("                        data=path.read_bytes()", "                        with path.open('rb') as stream:\n                            data=stream.read(65537)")
start=s.index("with (OUT/'stdout-stderr.log').open('wb') as log:")
s=s[:start]+'''class RunnerInterrupted(Exception):
    """An outer cancellation must still run this invocation's owned cleanup."""


def owned_session_runner():
    # Reuse the reviewed cross-platform SID enumerator; never infer ownership
    # from a process name or signal an unrelated process group.
    path = ROOT / 'scripts/run-v3-candidate-check.py'
    spec = importlib.util.spec_from_file_location('shakeout_owned_session', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def cleanup_verification(session, observer_stopped):
    result = {'observed_at':now(), 'owned_session':session,
              'recorded_processes':{}, 'ports':{}, 'observer_stopped':observer_stopped}
    try:
        for key, identity in owned.items():
            current = subprocess.run(['ps','-p',key,'-o','pid=,lstart=,comm='],
                                     capture_output=True,text=True,timeout=5)
            result['recorded_processes'][key] = {
                'name':identity['name'],
                'alive_same_identity':current.returncode == 0 and current.stdout.strip() == identity['ps_identity'],
                'current_identity':current.stdout.strip()}
        for port in range(15000,15064):
            with socket.socket() as probe:
                probe.settimeout(.2)
                result['ports'][str(port)] = {'open':probe.connect_ex(('127.0.0.1',port)) == 0}
        result['pass'] = bool(session['verified'] and session['complete'] and observer_stopped
            and not any(p['alive_same_identity'] for p in result['recorded_processes'].values())
            and not any(p['open'] for p in result['ports'].values()))
    except Exception as error:
        result.update({'pass':False, 'error_type':type(error).__name__, 'error':str(error)})
    return result


def final_report_receipt():
    path = RUN / 'report' / 'final.json'
    result = {'present':path.is_file(), 'readable':False}
    if result['present']:
        try:
            with path.open('rb') as stream:
                data = stream.read(1024*1024+1)
            if len(data) > 1024*1024:
                raise ValueError('final report exceeds 1 MiB summary bound')
            report = json.loads(data)
            result.update({'readable':True, 'sha256':hashlib.sha256(data).hexdigest(),
                           'final':report.get('final'), 'verdict':report.get('verdict')})
        except Exception as error:
            result.update({'error_type':type(error).__name__, 'error':str(error)})
    return result


with (OUT/'stdout-stderr.log').open('wb') as log:
    child = None
    status = None
    received_signal = None
    wrapper_error = None
    session_runner = None
    previous_signals = {}

    def interrupted(signum, _frame):
        global received_signal
        received_signal = signum
        raise RunnerInterrupted(f'outer runner received signal {signum}')

    try:
        previous_signals = {sig:signal.signal(sig,interrupted) for sig in (signal.SIGTERM,signal.SIGINT)}
        session_runner = owned_session_runner()
        child = subprocess.Popen(arguments,cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
        receipt['helper_pid'] = child.pid
        receipt['owned_session_id'] = child.pid
        write('command.json',receipt)
        observer.start()
        print(json.dumps({'started':now(),'helper_pid':child.pid,'source_sha':SOURCE,'run_dir':str(RUN)}),flush=True)
        try:
            status = child.wait(timeout=3000)
        except subprocess.TimeoutExpired:
            receipt['outer_deadline_exceeded'] = True
    except BaseException as error:
        wrapper_error = {'error_type':type(error).__name__, 'error':str(error)}
    finally:
        # A repeated TERM/INT cannot interrupt the owned-session cleanup. Hard
        # SIGKILL or host loss remains outside Python finally guarantees.
        for sig in previous_signals:
            signal.signal(sig,signal.SIG_IGN)
        session = {'session_id':None,'verified':True,'complete':True,
                   'initial_processes':[],'remaining_processes':[]}
        if child is not None:
            if child.poll() is None:
                try:
                    child.send_signal(signal.SIGTERM)
                    status = child.wait(timeout=120)
                except subprocess.TimeoutExpired:
                    receipt['forced_owned_session_stop'] = True
                except ProcessLookupError:
                    pass
            session = session_runner.cleanup_session(child.pid)
            try:
                status = child.wait(timeout=1)
            except subprocess.TimeoutExpired:
                session.update({'complete':False,'error':'helper did not exit after owned-session cleanup'})
        observer_stop.set()
        if observer.ident is not None:
            observer.join(timeout=15)
        cleanup = cleanup_verification(session,not observer.is_alive())
        # Persist the raw helper result and cleanup even if subsequent source,
        # report, or credential inspection encounters an independent failure.
        receipt.update({'exit_code':status,'finished_at':now(),
                        'outer_signal':received_signal,'wrapper_error':wrapper_error})
        write('cleanup-verification.json',cleanup)
        write('command.json',receipt)

post_errors = []
try:
    receipt.update({'source_sha_after':cmd(['git','rev-parse','HEAD']),
                    'source_clean_after':cmd(['git','status','--porcelain']) == '',
                    'source_files_unchanged':freeze == {name:sha(ROOT/name) for name in files}})
except Exception as error:
    post_errors.append({'stage':'source_verification','error_type':type(error).__name__,'error':str(error)})
write('observation-summary.json',{'process_capture_interval_seconds':5,'host_capture_interval_seconds':15,
    'host_ports':[15060,15061,15062,15063],'per_response_bytes_max':65536,'endpoint_timeout_seconds':2,
    'errors':observer_errors,'observer_stopped':not observer.is_alive(),
    'source':'Existing loopback host-metrics and process.prom files only; no product metrics scrape or profile capture.'})
final_report = final_report_receipt()
write('final-report-status.json',final_report)
# Persist only paths and presence flags; never raw matching secret bytes.
leaks=[]
try:
    candidates=[]
    for base in [RUN/'logs',RUN/'config',RUN/'report',RUN/'evidence',observations]:
        if base.exists():
            candidates.extend(f for f in base.rglob('*') if f.is_file())
    candidates.extend(f for f in OUT.iterdir() if f.is_file())
    for f in candidates:
        if f.stat().st_size <= 64*1024*1024:
            with f.open('rb') as stream:
                data=stream.read(64*1024*1024+1)
            if len(data)>64*1024*1024:
                raise ValueError('credential scan input grew beyond its 64 MiB bound')
            if api_token.encode() in data or worker_token.encode() in data:
                leaks.append(str(f.relative_to(OUT)))
except Exception as error:
    post_errors.append({'stage':'credential_scan','error_type':type(error).__name__,'error':str(error)})
write('credential-scan.json',{'raw_token_match_paths':leaks,
    'scope':'Uncompressed regular logs/config/report/evidence/observations files and top-level evidence files <=64MiB. No token values saved.',
    'pass':not leaks and not any(e['stage'] == 'credential_scan' for e in post_errors)})
source_ok = receipt.get('source_sha_after') == SOURCE and receipt.get('source_clean_after') is True and receipt.get('source_files_unchanged') is True
if received_signal is not None:
    wrapper_exit = 128 + received_signal
elif receipt.get('outer_deadline_exceeded'):
    wrapper_exit = 124
elif wrapper_error or post_errors or observer_errors or not cleanup['pass'] or leaks or not source_ok:
    wrapper_exit = status if status not in (None,0,130) and status > 0 else 1
else:
    wrapper_exit = (status if status >= 0 else 128-status) if status is not None else 1
planned_stop = (status == 130 and received_signal is None and not receipt.get('outer_deadline_exceeded')
                and final_report.get('final') is True and (final_report.get('verdict') or {}).get('outcome') == 'operator_stop')
receipt.update({'wrapper_exit_code':wrapper_exit,'post_errors':post_errors,
                'helper_planned_operator_stop':planned_stop,'final_report':final_report,
                'cleanup_pass':cleanup['pass'],'observer_errors':observer_errors,
                'credential_scan_pass':not leaks and not any(e['stage'] == 'credential_scan' for e in post_errors)})
write('command.json',receipt)
write('result.json',{'helper_exit_code':status,'wrapper_exit_code':wrapper_exit,
                    'helper_planned_operator_stop':planned_stop,'outer_signal':received_signal,
                    'outer_deadline_exceeded':bool(receipt.get('outer_deadline_exceeded')),
                    'wrapper_error':wrapper_error,'post_errors':post_errors,
                    'final_report':final_report,'cleanup':cleanup,'observer_errors':observer_errors,
                    'credential_scan_pass':receipt['credential_scan_pass'],'source_unchanged':source_ok})
for sig,handler in previous_signals.items():
    signal.signal(sig,handler)
print(json.dumps({'finished':now(),'helper_exit_code':status,'wrapper_exit_code':wrapper_exit,
                  'helper_planned_operator_stop':planned_stop,'cleanup_pass':cleanup['pass'],
                  'raw_token_match_paths':leaks}),flush=True)
raise SystemExit(wrapper_exit)
'''
p.write_text(s)
