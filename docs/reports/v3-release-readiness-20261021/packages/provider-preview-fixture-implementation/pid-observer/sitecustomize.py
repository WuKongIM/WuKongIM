"""Scratch diagnostic only: observe real public-certificate GPG imports.

No result or verifier is substituted. Private-key homes/commands and command
input/stdout are never recorded. This instrumented run is not positive acceptance.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

_actual_run = subprocess.run


def _read(path):
    try:
        return Path(path).read_text()[:2048]
    except OSError as error:
        return type(error).__name__


def _snapshot(command, result):
    processes = []
    for path in Path('/proc').iterdir():
        if not path.name.isdecimal():
            continue
        try:
            name = (path / 'comm').read_text().strip()
            stat = (path / 'stat').read_text().rsplit(')', 1)[1].split()
            if name == 'gpg-agent' or path.name == '1':
                processes.append({'pid': int(path.name), 'ppid': int(stat[1]), 'state': stat[0], 'comm': name})
        except (OSError, IndexError):
            pass
    certificate = Path(command[-1])
    try:
        raw = certificate.read_bytes()
        cert = {'path': str(certificate), 'size': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}
    except OSError as error:
        cert = {'path': str(certificate), 'error': type(error).__name__}
    stderr = result.stderr if isinstance(result.stderr, bytes) else (result.stderr or '').encode()
    return {'time_unix': time.time(), 'observer_pid': os.getpid(), 'command': list(map(str, command)),
        'exit_code': result.returncode, 'stderr_size': len(stderr),
        'stderr_sha256': hashlib.sha256(stderr).hexdigest(),
        'failure_stderr': stderr[:4096].decode(errors='replace') if result.returncode else None,
        'certificate': cert, 'processes': processes,
        'gpg_agent_live': sum(p['comm'] == 'gpg-agent' and p['state'] != 'Z' for p in processes),
        'gpg_agent_zombies': sum(p['comm'] == 'gpg-agent' and p['state'] == 'Z' for p in processes),
        'public_gpg_home_count': len(list(Path('/tmp').glob('wk-site-*-gpg-*'))),
        'cgroup': {name: _read('/sys/fs/cgroup/' + name) for name in
                   ('memory.events', 'memory.current', 'memory.max', 'pids.current', 'pids.max', 'pids.events')}}


def _observed_run(*args, **kwargs):
    result = _actual_run(*args, **kwargs)
    command = args[0] if args else kwargs.get('args')
    if (isinstance(command, list) and command and Path(str(command[0])).name == 'gpg'
            and '--homedir' in command and '--import' in command
            and str(command[command.index('--homedir') + 1]).startswith('/tmp/wk-site-')
            and str(command[-1]).endswith('.asc')):
        try:
            path = Path('/observer-output/gpg-observations.jsonl')
            if not path.exists() or path.stat().st_size < 2 * 1024 * 1024:
                record = _snapshot(command, result)
                descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o644)
                try:
                    os.write(descriptor, json.dumps(record, separators=(',', ':')).encode() + b'\n')
                finally:
                    os.close(descriptor)
        except Exception:
            pass
    return result


subprocess.run = _observed_run
