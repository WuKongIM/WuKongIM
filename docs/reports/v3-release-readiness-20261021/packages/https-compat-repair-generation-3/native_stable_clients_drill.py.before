#!/usr/bin/env python3
"""TEST ONLY stable clients: signed A/B, real HTTPS, and installed product CLI.

This laboratory never publishes, enables a channel, or uses production secrets.
Only the fixture signing phase owns short-lived TEST ONLY OpenPGP keys. The
coordinator receives public candidate exports after those keys are destroyed.
"""
from __future__ import annotations

import argparse
import email.utils
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import re
import selectors
import shutil
import signal
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
PRODUCT_SHA = 'fc18221282f289088ec9b4282fda8e0bfc67a3b8'
ORIGIN = 'https://packages.githubim.com'
MIRROR = '/rpm/stable/el/9/x86_64/mirrorlist'
INRELEASE = '/apt/dists/stable/InRelease'
MAX_LOG = 8 * 1024 * 1024
MAX_REQUESTS = 20000
CORRUPT_APT_INDEX = b'changed authenticated index\n'


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode() + b'\n'


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def facts(path):
    raw = path.read_bytes()
    return {'size': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}


def module(filename):
    spec = importlib.util.spec_from_file_location('stable_clients_' + Path(filename).stem.replace('-', '_'), ROOT / filename)
    value = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = value
    spec.loader.exec_module(value)
    return value


def run_command(evidence, stage, command, *, timeout=300, maximum=MAX_LOG, env=None, cwd=None):
    """Bound public subprocess output and time while preserving its actual exit."""
    record = {'stage': stage, 'command': list(map(str, command)), 'exit_code': None,
              'timed_out': False, 'output_limit': False}
    evidence['commands'].append(record)
    process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, env=env, cwd=cwd, start_new_session=True)
    output = bytearray()
    start = time.monotonic()
    try:
        with selectors.DefaultSelector() as ready:
            ready.register(process.stdout, selectors.EVENT_READ)
            while ready.get_map():
                if time.monotonic() - start > timeout:
                    record['timed_out'] = True
                    raise RuntimeError('command exceeded its bounded deadline: ' + stage)
                for key, _ in ready.select(min(0.1, timeout)):
                    chunk = os.read(key.fileobj.fileno(), 65536)
                    if not chunk:
                        ready.unregister(key.fileobj)
                    else:
                        if len(output) + len(chunk) > maximum:
                            record['output_limit'] = True
                            raise RuntimeError('command exceeded its output limit: ' + stage)
                        output.extend(chunk)
        process.wait(timeout=max(0.01, timeout - (time.monotonic() - start)))
        return subprocess.CompletedProcess(command, process.returncode, bytes(output), b'')
    finally:
        if process.poll() is None or sys.exc_info()[0] is not None:
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL); process.wait(timeout=2)
        process.stdout.close()
        record.update(exit_code=process.returncode, elapsed_seconds=round(time.monotonic() - start, 3),
                      output_size=len(output), output_sha256=hashlib.sha256(output).hexdigest(),
                      output_tail=bytes(output[-4096:]).decode(errors='replace'))


def apt_by_hash_path(path):
    """Recognize immutable APT index paths for both native strong-hash choices."""
    return re.fullmatch(r'/apt/.*/by-hash/(?:SHA256/[0-9a-f]{64}|SHA512/[0-9a-f]{128})', path) is not None


def verify_apt_by_hash_fault(mode, observed):
    """Require the intended fault response in addition to native client rejection."""
    require(mode in ('missing-byhash', 'corrupt-byhash'), 'unsupported by-hash fault mode')
    for item in observed:
        if not apt_by_hash_path(item['path']):
            continue
        if ((mode == 'missing-byhash' and item['status'] == 404)
                or (mode == 'corrupt-byhash' and item['status'] == 200
                    and item['sha256'] == hashlib.sha256(CORRUPT_APT_INDEX).hexdigest())):
            return item
    raise RuntimeError('native rejection lacks the actual by-hash fault response')


class RouteTable:
    """Serve only frozen planner objects; switch after the exact response barrier."""
    def __init__(self, root, maps):
        self.root = root
        self.maps = maps
        self.serial = None
        self.lock = threading.Lock()

    def configure(self, state):
        if self.serial != state['serial']:
            require(state['generation'] in ('A', 'B'), 'invalid test generation')
            self.serial = state['serial']
            self.generation = state['generation']
            self.mode = state['mode']
            self.switched = False

    def response(self, path, headers):
        with self.lock:
            parts = PurePosixPath(path).parts
            if '?' in path or '%' in path or '..' in parts or not path.startswith('/'):
                return 404, b'', {}
            value = self.maps[self.generation].get(path)
            if value is None:
                return 404, b'', {}
            raw = (self.root / value['file']).read_bytes()
            result = {'Cache-Control': value.get('cache_control', 'no-store'),
                      'ETag': '"' + hashlib.sha256(raw).hexdigest() + '"',
                      'Last-Modified': email.utils.formatdate(1700000000 + int(hashlib.sha256(raw).hexdigest()[:6], 16), usegmt=True)}
            if self.mode == 'redirect' and path in (INRELEASE, MIRROR):
                return 302, b'', {'Location': 'https://forbidden.invalid/object'}
            if self.mode == 'wrong-mirror' and path == MIRROR:
                raw = b'https://forbidden.invalid/repodata/\n'
            if self.mode == 'missing-byhash' and apt_by_hash_path(path):
                return 404, b'', result
            if self.mode == 'corrupt-byhash' and apt_by_hash_path(path):
                raw = CORRUPT_APT_INDEX
            if self.mode == 'missing-metadata' and '/repodata/' in path and not path.endswith(('repomd.xml', 'repomd.xml.asc')):
                return 404, b'', result
            if self.mode == 'wrong-asc' and path.endswith('/repomd.xml.asc'):
                other = next((v for p, v in self.maps['B'].items()
                              if p.endswith('/repomd.xml.asc') and '/r4243-' in p), None)
                require(other is not None, 'missing B signature fault fixture')
                raw = (self.root / other['file']).read_bytes()
            if self.mode == 'disconnect' and path in (INRELEASE, MIRROR):
                result['X-Test-Disconnect'] = 'true'
            if self.mode == 'normal' and (headers.get('If-None-Match') == result['ETag']
                    or headers.get('If-Modified-Since') == result['Last-Modified']):
                return 304, b'', result
            if ((self.mode in ('apt-switch', 'missing-byhash', 'corrupt-byhash') and path == INRELEASE)
                    or (self.mode == 'rpm-switch' and path == MIRROR)) and not self.switched:
                self.generation = 'B'
                self.switched = True
            return 200, raw, result


def serve(root):
    maps = json.loads((root / 'routes.json').read_bytes())
    routes = RouteTable(root, maps)
    class TLSServer(ThreadingHTTPServer):
        def shutdown_request(self, request):
            # A TCP half-close alone violates TLS: strict OpenSSL clients reject
            # even complete HTTP bodies without close_notify. Bound the peer's
            # reciprocal shutdown; truncation faults still fail Content-Length.
            try:
                request.settimeout(2)
                request = request.unwrap()
            except OSError:
                pass
            finally:
                super().shutdown_request(request)
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass
        def do_GET(self):
            try:
                state = json.loads((root / 'state.json').read_bytes())
                routes.configure(state)
                status, raw, headers = routes.response(self.path, self.headers)
                with routes.lock:
                    if self.server.count >= MAX_REQUESTS:
                        raise RuntimeError('request count limit exceeded')
                    self.server.count += 1
                    with (root / 'requests.jsonl').open('ab') as log:
                        log.write(canonical({'serial': state['serial'], 'mode': state['mode'], 'path': self.path, 'status': status,
                            'sha256': hashlib.sha256(raw).hexdigest(), 'size': len(raw),
                            'if_none_match': self.headers.get('If-None-Match'),
                            'if_modified_since': self.headers.get('If-Modified-Since'),
                            'cache_control': headers.get('Cache-Control'), 'etag': headers.get('ETag')}))
                self.send_response(status)
                for key, value in headers.items():
                    if not key.startswith('X-Test-'):
                        self.send_header(key, value)
                self.send_header('Content-Length', str(len(raw) + (7 if 'X-Test-Disconnect' in headers else 0)))
                self.end_headers()
                self.wfile.write(raw)
                if 'X-Test-Disconnect' in headers:
                    self.close_connection = True
            except (BrokenPipeError, ConnectionResetError):
                pass
            except Exception:
                (root / 'server-error').write_text('bounded HTTPS handler failed\n')
                self.close_connection = True
    server = TLSServer(('0.0.0.0', 443), Handler)
    server.count = 0
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(root / 'tls.crt', root / 'tls.key')
    wrong_name = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    wrong_name.load_cert_chain(root / 'wrong-name.crt', root / 'wrong-name.key')
    def select_certificate(connection, hostname, initial):
        if json.loads((root / 'state.json').read_bytes())['mode'] == 'wrong-san':
            connection.context = wrong_name
    context.set_servername_callback(select_certificate)
    server.socket = context.wrap_socket(server.socket, server_side=True)
    try:
        server.serve_forever()
    finally:
        server.server_close()


class OwnedDocker:
    """Recover ambiguous creations only through this run's UUID label and names."""
    def __init__(self, label, run=subprocess.run):
        self.label = label
        self.run = run
        self.containers = set()
        self.network = None
        self.attempts = {'containers': set(), 'networks': set()}
        self.cleanup_receipt = None
        self.cleanup_errors = []

    def create(self, kind, command, checked):
        require(kind in ('container', 'network'), 'invalid owned resource kind')
        require('wk-stable-client-run=' + self.label in command, 'owned creation lacks its exact label')
        name = command[command.index('--name') + 1] if kind == 'container' else command[-1]
        require(name.startswith('wk-scl-' + self.label), 'owned creation name differs')
        # Register before the CLI can reach the daemon. A timeout, TERM or bad
        # return body must not erase the possibility that creation succeeded.
        self.attempts[kind + 's'].add(name)
        identifier = checked('create-' + kind, command).decode().strip()
        require(re.fullmatch('[0-9a-f]{64}', identifier), 'Docker returned invalid resource identity')
        if kind == 'container': self.containers.add(identifier)
        else: self.network = identifier
        return identifier

    def cleanup(self, *, final=True):
        if self.cleanup_receipt is not None:
            return self.cleanup_receipt
        errors = list(self.cleanup_errors)
        attempted = bool(self.containers or self.network or any(self.attempts.values()))
        def invoke(command):
            try:
                value = self.run(command, capture_output=True, timeout=20)
                if len(value.stdout) > 131072 or len(value.stderr) > 131072:
                    errors.append('cleanup output limit exceeded'); return None
                return value
            except (OSError, subprocess.SubprocessError):
                errors.append('cleanup command unavailable'); return None
        def inventory():
            result = {'containers': [], 'networks': []}
            for kind, command in [('containers', ['docker', 'ps', '-aq', '--no-trunc']),
                                  ('networks', ['docker', 'network', 'ls', '-q', '--no-trunc'])]:
                value = invoke([*command, '--filter', 'label=wk-stable-client-run=' + self.label])
                if value is None or value.returncode:
                    errors.append(kind + ' inventory failed'); continue
                identifiers = value.stdout.decode(errors='replace').split()
                if len(identifiers) > 256 or any(not re.fullmatch('[0-9a-f]{64}', item) for item in identifiers):
                    errors.append(kind + ' inventory is invalid'); continue
                result[kind] = identifiers
            return result
        initial = inventory() if attempted else {'containers': [], 'networks': []}
        containers = set(initial['containers']) | self.containers
        networks = set(initial['networks']) | ({self.network} if self.network else set())
        for kind, identifiers in (('container', containers), ('network', networks)):
            for identifier in sorted(identifiers):
                label_path = '.Config.Labels' if kind == 'container' else '.Labels'
                format_value = '{{.Id}} {{.Name}} {{index ' + label_path + ' "wk-stable-client-run"}}'
                command = (['docker', 'inspect', '--type', 'container'] if kind == 'container'
                           else ['docker', 'network', 'inspect'])
                metadata = invoke([*command, '--format', format_value, identifier])
                if metadata is None: continue
                if metadata.returncode:
                    if b'No such' not in metadata.stderr: errors.append(kind + ' identity query failed')
                    continue
                fields = metadata.stdout.decode(errors='replace').split()
                if (len(fields) != 3 or fields[0] != identifier or fields[2] != self.label
                        or fields[1].lstrip('/') not in self.attempts[kind + 's']):
                    errors.append(kind + ' identity does not match this run'); continue
                command = ['docker', 'rm', '--force', identifier] if kind == 'container' else ['docker', 'network', 'rm', identifier]
                removed = invoke(command)
                if removed is not None and removed.returncode == 0:
                    # Retire active IDs after confirmed removal. Keep creation
                    # attempts so later stages must still query the UUID label.
                    if kind == 'container': self.containers.discard(identifier)
                    elif self.network == identifier: self.network = None
                elif removed is not None and b'No such' not in removed.stderr:
                    errors.append(kind + ' removal failed')
        # This is mandatory after every attempted create, even when no create
        # call returned an ID. NoSuch is accepted only if this final check is empty.
        remaining = inventory() if attempted else {'containers': [], 'networks': []}
        receipt = {'verified': not errors and not any(remaining.values()),
            'creation_attempts': {kind: sorted(names) for kind, names in self.attempts.items()},
            'discovered': initial, 'remaining': remaining, 'errors': errors}
        self.cleanup_errors = errors
        if final: self.cleanup_receipt = receipt
        return receipt


def fixture_user():
    require(os.getuid() != 0, 'fixture export requires a non-root host user')
    return str(os.getuid()) + ':' + str(os.getgid())


def cache_mounts(family):
    """DNF's cachedir and persistdir are both relative to its explicit installroot."""
    require(family in ('apt', 'rpm'), 'invalid cache family')
    return ([('apt', '/var/lib/apt'), ('apt-cache', '/var/cache/apt')] if family == 'apt' else
            [('dnf-cache', '/tmp/client-root/tmp/dnf-cache'),
             ('dnf-state', '/tmp/client-root/tmp/dnf-state')])


def cache_inventory(root, *, arm=False):
    """Hash bounded cache files; capture atime before our own read, then optionally arm it."""
    result = {}; total = 0
    if not root.exists(): return result
    for path in sorted(root.rglob('*')):
        info = path.lstat()
        require(not path.is_symlink(), 'cache must not contain links')
        if path.is_dir(): continue
        require(path.is_file() and info.st_nlink == 1 and info.st_size <= 64 * 1024 * 1024,
                'cache contains an unsupported file')
        total += info.st_size
        require(len(result) < 10000 and total <= 512 * 1024 * 1024, 'cache inventory exceeds bounds')
        if not info.st_size: continue
        relative = path.relative_to(root).as_posix()
        result[relative] = {**facts(path), 'device': info.st_dev, 'inode': info.st_ino,
                            'mtime_ns': info.st_mtime_ns, 'atime_ns': info.st_atime_ns}
        if arm:
            # Preserve content/mtime. Under Linux relatime this old atime must
            # advance on a real subsequent read. A noatime filesystem fails the
            # evidence gate rather than silently treating mere presence as reuse.
            os.utime(path, ns=(1, info.st_mtime_ns), follow_symlinks=False)
    return result


def cache_evidence(before, after, *, require_reuse):
    def metadata(name):
        return (name.startswith('apt/lists/') and '/partial/' not in name
                or name.startswith('dnf-cache/') and ('/repodata/' in name or name.endswith(('.solv', '.solvx'))))
    present = [name for name in after if metadata(name)]
    require(present, 'persistent cache did not contain nonempty metadata')
    reads = [name for name in present if name in before and after[name]['atime_ns'] > 1
             and all(before[name][key] == after[name][key] for key in ('device', 'inode', 'sha256', 'size'))]
    require(not require_reuse or reads, 'persistent cache did not prove a read of existing metadata')
    return {'before': before, 'after': after, 'metadata_reads': reads, 'reuse_required': require_reuse,
            'reuse_signal': 'same inode and content; atime advanced after epoch arming before native execution'}


def cache_operation(root, operation):
    """Run only inside the owned cache helper; do not change cached content or mtime."""
    require(operation in ('inventory', 'arm', 'remove'), 'invalid cache operation')
    require(root.is_dir() and not root.is_symlink(), 'cache root must be a real directory')
    if operation != 'remove': return cache_inventory(root, arm=operation == 'arm')
    for path in root.iterdir():
        if path.is_dir() and not path.is_symlink(): shutil.rmtree(path)
        else: path.unlink()
    return {'remaining': sorted(path.name for path in root.iterdir())}


def cache_helper_command(image, root, operation):
    require(operation in ('inventory', 'arm', 'remove'), 'invalid cache operation')
    # Root is confined to one task cache mount. No client credentials, public
    # fixture signing tree, network, Docker socket or host-wide path is exposed.
    code = ('import importlib.util,sys\n'
            'from pathlib import Path\n'
            's=importlib.util.spec_from_file_location("cache_helper","/control/tests/native_stable_clients_drill.py")\n'
            'm=importlib.util.module_from_spec(s);s.loader.exec_module(m)\n'
            'sys.stdout.buffer.write(m.canonical(m.cache_operation(Path("/cache"),sys.argv[1])))')
    return ['docker', 'run', '--platform', 'linux/amd64', '--network', 'none', '--read-only',
            '--cap-drop', 'ALL', '--cap-add', 'DAC_OVERRIDE', '--cap-add', 'FOWNER',
            '--security-opt', 'no-new-privileges', '--user', '0:0', '--pids-limit', '32',
            '--memory', '256m', '--cpus', '1', '--tmpfs', '/tmp:rw,nosuid,nodev,size=16m',
            '--volume', str(ROOT) + ':/control:ro', '--volume', str(root) + ':/cache:rw',
            image, 'python3', '-B', '-c', code, operation]


def installed_negative_command(validator, image, package, output, certificate, fault):
    """Fault only a real throwaway installation, retaining the original acceptance program."""
    require(fault in ('wrong-commit', 'old-version', 'missing-wkcli'), 'unsupported installed fault')
    identity = {'version': '3.0.1' if fault == 'old-version' else '3.0.0',
                'commit': '0' * 40 if fault == 'wrong-commit' else PRODUCT_SHA, 'build_source': 'release'}
    command = validator._installed_cli_command(image, package, output, certificate, 'apt', identity, facts(package)['sha256'])
    if fault == 'missing-wkcli':
        marker = 'python3 /acceptance.py --fixtures /fixtures'
        require(command[-1].count(marker) == 1, 'installed acceptance command changed')
        injection = ('test -x /usr/bin/wukongim\ntest -x /usr/bin/wkcli\n'
                     'rm -- /usr/bin/wkcli\ntest ! -e /usr/bin/wkcli\n'
                     'printf "%s\\n" "TEST ONLY removed wkcli after verified installation"\n')
        command[-1] = command[-1].replace(marker, injection + marker)
    return command


def build_product(args, scratch, evidence):
    require(args.product_root is not None and args.product_root.is_dir(), 'fixed product checkout is required')
    def checked(stage, command, **kwargs):
        result = run_command(evidence, stage, command, **kwargs)
        require(result.returncode == 0, 'product fixture command failed: ' + stage)
        return result.stdout.decode().strip()
    sha = checked('product-commit', ['git', '-C', str(args.product_root), 'rev-parse', 'HEAD'])
    require(sha == PRODUCT_SHA, 'product checkout differs from fixed source')
    require(not checked('product-clean', ['git', '-C', str(args.product_root), 'status', '--porcelain']),
            'product checkout must be clean')
    tree = checked('product-tree', ['git', '-C', str(args.product_root), 'rev-parse', 'HEAD^{tree}'])
    version = checked('go-version', ['go', 'version'])
    require('go1.25.11 ' in version, 'fixed Go 1.25.11 toolchain is required')
    binaries = scratch / 'binaries'; binaries.mkdir()
    env = {**os.environ, 'GOWORK': 'off', 'CGO_ENABLED': '0', 'GOOS': 'linux', 'GOARCH': 'amd64', 'GOFLAGS': '-p=2 -mod=readonly'}
    receipts = {}
    for release in ('3.0.0', '3.0.1'):
        directory = binaries / release; directory.mkdir()
        for name in ('wukongim', 'wkcli'):
            path = directory / name
            flags = f'-s -w -X main.buildVersion={release} -X main.buildCommit={PRODUCT_SHA} -X main.buildSource=release'
            checked('build-' + release + '-' + name,
                    ['go', 'build', '-trimpath', '-ldflags', flags, '-o', str(path), './cmd/' + name],
                    cwd=args.product_root, env=env, timeout=900)
            receipts[release + '/' + name] = {**facts(path), 'go_version_m': checked(
                'build-info-' + release + '-' + name, ['go', 'version', '-m', str(path)])}
    require(receipts['3.0.0/wukongim']['sha256'] != receipts['3.0.1/wukongim']['sha256']
            and receipts['3.0.0/wkcli']['sha256'] != receipts['3.0.1/wkcli']['sha256'], 'A/B product bytes must differ')
    require(not checked('product-still-clean', ['git', '-C', str(args.product_root), 'status', '--porcelain']),
            'product build changed its source checkout')
    evidence['product_build'] = {'source_sha': sha, 'tree': tree, 'go_version': version,
                                 'test_only_versions': True, 'binaries': receipts}
    return binaries


def seal_generations(exports, fixtures, scratch, evidence):
    """Exercise existing writers only against the explicit loopback API substitute."""
    sys.path.insert(0, str(ROOT)); sys.path.insert(0, str(ROOT / 'tests'))
    from stable_audit_fixture import AuditState, serve_audit
    from test_plan_object_storage_publication import InputSnapshot
    preview = InputSnapshot(scratch / 'preview-structural')
    descriptors = {}
    for label, release_id in (('A', 4242), ('B', 4243)):
        entry = exports['generations'][label]
        paths = {key: fixtures / entry[key] for key in
                 ('control', 'source', 'plan', 'archive', 'receipt', 'apt_receipt', 'rpm_receipt', 'site')}
        state = AuditState(release_id=release_id)
        def invoke(filename, args):
            result = run_command(evidence, label + '-' + filename,
                                 [sys.executable, '-B', str(ROOT / 'scripts' / filename), *map(str, args)],
                                 env={**os.environ, 'WK_TEST_AUDIT_TOKEN': 'TEST ONLY loopback token'})
            require(result.returncode == 0, 'TEST ONLY seal operation failed: ' + filename)
            return json.loads(result.stdout)
        with serve_audit(state) as base:
            common = ['--channel', 'stable', '--test-only', '--release-id', release_id,
                      '--expected-control-sha', 'a' * 40, '--api-base-url', base,
                      '--token-env', 'WK_TEST_AUDIT_TOKEN', '--candidate-plan', paths['plan']]
            invoke('bind-audit-release.py', [*common, '--expected-previous-control-sha', 'a' * 40,
                                           '--control-root', paths['control']])
            invoke('seal-audit-release.py', [*common, '--download-base-url', base,
                '--archive', paths['archive'], '--receipt', paths['receipt'], '--control-root', paths['control'],
                '--candidate-source-root', paths['source'], '--apt-receipt', paths['apt_receipt'],
                '--rpm-receipt', paths['rpm_receipt']])
            resolution = invoke('resolve-audit-release.py', [*common, '--download-base-url', base,
                '--candidate-receipt', paths['receipt'], '--output-dir', scratch / ('resolved-' + label),
                '--expected-tag-state', 'exact'])
        resolution_path = scratch / ('resolution-' + label + '.json')
        resolution_path.write_bytes(canonical(resolution))
        descriptors[label] = {'channel': 'stable', 'audit_release_id': release_id, 'seal_control_sha': 'a' * 40,
            'candidate': resolution['candidate'], 'resolution': str(resolution_path),
            'resolution_sha256': facts(resolution_path)['sha256'],
            **{name: str(paths[name]) for name in ('archive', 'receipt', 'plan', 'apt_receipt', 'rpm_receipt')},
            'control_dir': str(paths['control']), 'source_dir': str(paths['source'])}
    require(descriptors['A']['candidate']['archive']['sha256'] != descriptors['B']['candidate']['archive']['sha256'],
            'A/B must be different signed candidate archives')
    planner = module('scripts/plan-object-storage-publication.py')
    plans = {}; inputs = {}
    for label, other in (('A', 'B'), ('B', 'A')):
        path = scratch / ('inputs-' + label + '.json')
        path.write_bytes(canonical({'schema': 'wukongim.object_storage_plan_inputs/v2', 'channel': 'composite',
            'test_only': True, 'current': {'preview': {'channel': 'preview', 'snapshot': preview.descriptor()},
                                        'stable': descriptors[label]}, 'retain': [descriptors[other]]}))
        inputs[label] = path
        plans[label] = planner.make_composite_plan(path, True)
    return descriptors, plans, inputs, preview


def materialize_routes(plans, descriptors, preview, root):
    """Copy only actual checked archive members into a content-addressed HTTPS fixture."""
    archive_module = module('scripts/archive-package-snapshot.py')
    sources = {10: preview.archive, **{value['audit_release_id']: Path(value['archive']) for value in descriptors.values()}}
    extracts = root / 'extracted'; extracts.mkdir()
    for release_id, archive in sources.items():
        archive_module.extract_snapshot(archive_path=archive, output_dir=extracts / str(release_id),
            max_members=archive_module.DEFAULT_MAX_MEMBERS, max_total_size=2 * 1024 * 1024 * 1024)
    objects = root / 'objects'; objects.mkdir()
    routes = {}
    for label, plan in plans.items():
        mapping = {}; by_key = {}
        def copy(raw, item):
            require(len(raw) == item['size'] and hashlib.sha256(raw).hexdigest() == item['sha256'],
                    'fixture object differs from verified plan')
            destination = objects / item['sha256']
            if not destination.exists(): destination.write_bytes(raw)
            value = {'file': str(destination.relative_to(root)), 'cache_control': item['cache_control']}
            by_key[item['object_key']] = value
            return value
        for item in plan['snapshot_objects']:
            source = item['source']
            value = copy((extracts / str(source['audit_release_id']) / source['member']).read_bytes(), item)
            mapping['/' + item['object_key']] = value
        for item in plan['generated_objects']:
            copy(item['content_utf8'].encode(), item)
        for item in plan['immutable_aliases']:
            source = item['source']
            mapping[item['public_uri']] = copy(
                (extracts / str(source['audit_release_id']) / source['member']).read_bytes(), item)
        for item in plan['entrypoints']:
            mapping[item['public_uri']] = by_key[item['object_key']]
        routes[label] = mapping
    (root / 'routes.json').write_bytes(canonical(routes))
    shutil.rmtree(extracts)
    return routes


def exercise(args, evidence, owned):
    require(sys.platform == 'linux', 'native stable clients require a Linux Docker host')
    require(args.trusted_root is not None and args.trusted_root.is_dir(), 'trusted repository tools are required')
    for tool in ('docker', 'git', 'go', 'openssl', 'gpg', 'gpgv', 'rpm', 'rpmkeys', 'dpkg-deb', 'zstd'):
        require(shutil.which(tool), 'required native tool unavailable: ' + tool)
    export_user = fixture_user()
    validator = module('scripts/validate-production-package-clients.py')
    toolchain = json.loads((ROOT / 'manifests/signing-toolchain.json').read_bytes())
    image = toolchain['image'] + '@' + toolchain['digest']
    evidence['images'] = [image, *[item[1] for item in validator.APT_CLIENTS + validator.RPM_CLIENTS]]
    def checked(stage, command, **kwargs):
        result = run_command(evidence, stage, command, **kwargs)
        require(result.returncode == 0, 'native stage failed: ' + stage)
        return result.stdout
    evidence['control'] = {'checkout_sha': checked('control-head', ['git', '-C', str(ROOT), 'rev-parse', 'HEAD']).decode().strip(),
                           'tree': checked('control-tree', ['git', '-C', str(ROOT), 'rev-parse', 'HEAD^{tree}']).decode().strip()}
    for item in evidence['images']:
        checked('image-present', ['docker', 'image', 'inspect', item], maximum=1024 * 1024)
    evidence['host_tools'] = {}
    for tool in ('gpg', 'gpgv', 'rpm', 'rpmkeys', 'dpkg-deb', 'zstd'):
        evidence['host_tools'][tool] = checked('version-' + tool, [tool, '--version']).decode()[:4096]
    with tempfile.TemporaryDirectory(prefix='wk-scl-', dir='/tmp') as temporary:
        cache_roots = []
        try:
            scratch = Path(temporary)
            binaries = build_product(args, scratch, evidence)
            fixtures = scratch / 'fixtures'; fixtures.mkdir(mode=0o777); fixtures.chmod(0o777)
            serial = 0
            def create(command):
                nonlocal serial
                require(command[:2] == ['docker', 'run'], 'only explicit Docker run commands are accepted')
                serial += 1
                tokens = [token for token in command[2:] if token != '--rm']
                create_command = ['docker', 'create', '--name', 'wk-scl-' + owned.label + '-' + str(serial),
                                  '--label', 'wk-stable-client-run=' + owned.label, *tokens]
                return owned.create('container', create_command, checked)
            def cache_tool(root, operation):
                identifier = create(cache_helper_command(image, root, operation))
                raw = checked('cache-' + operation, ['docker', 'start', '--attach', identifier], timeout=60)
                value = json.loads(raw)
                require(isinstance(value, dict), 'cache helper returned invalid evidence')
                return value
            fixture_id = create(['docker', 'run', '--platform', 'linux/amd64', '--network', 'none', '--read-only',
                '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--user', export_user,
                '--cpus', '2', '--memory', '4g', '--pids-limit', '256',
                '--tmpfs', '/tmp:rw,nosuid,nodev,size=2147483648',
                '--volume', str(ROOT) + ':/control:ro', '--volume', str(binaries) + ':/binaries:ro',
                '--volume', str(args.trusted_root.resolve()) + ':/trusted:ro',
                '--volume', str(fixtures) + ':/public', image, 'python3', '-B',
                '/control/tests/native_stable_clients_drill.py', '--export-fixtures', '/public',
                '--binary-root', '/binaries', '--trusted-root', '/trusted'])
            checked('export-public-fixtures', ['docker', 'start', '--attach', fixture_id], timeout=900)
            exports = json.loads((fixtures / 'export.json').read_bytes())
            require(exports['secret_tree_removed'] is True and exports['source_sha'] == PRODUCT_SHA,
                    'public export lacks secret-removal or source evidence')
            evidence['fixture'] = exports
            descriptors, plans, inputs, preview = seal_generations(exports, fixtures, scratch, evidence)
            public = scratch / 'https'; public.mkdir(mode=0o777); public.chmod(0o777)
            routes = materialize_routes(plans, descriptors, preview, public)
            for common_key in ('apt', 'rpm'):
                a = fixtures / exports['generations']['A']['control'] / ('keys/' + common_key + '-stable.asc')
                b = fixtures / exports['generations']['B']['control'] / ('keys/' + common_key + '-stable.asc')
                require(a.read_bytes() == b.read_bytes(), 'A/B must share the same TEST ONLY public certificates')
            checked('test-TLS-certificate', ['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                '-keyout', str(public / 'tls.key'), '-out', str(public / 'tls.crt'), '-days', '1',
                '-subj', '/CN=packages.githubim.com', '-addext', 'subjectAltName=DNS:packages.githubim.com'])
            (public / 'tls.key').chmod(0o644)
            # This ephemeral CA is unrelated to production signing keys and is never archived.
            checked('wrong-test-CA', ['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                '-keyout', str(public / 'wrong.key'), '-out', str(public / 'wrong.crt'), '-days', '1',
                '-subj', '/CN=untrusted.invalid'])
            checked('wrong-name-request', ['openssl', 'req', '-new', '-newkey', 'rsa:2048', '-nodes',
                '-keyout', str(public / 'wrong-name.key'), '-out', str(public / 'wrong-name.csr'),
                '-subj', '/CN=wrong-name.invalid'])
            (public / 'wrong-name.ext').write_text('subjectAltName=DNS:wrong-name.invalid\n')
            checked('wrong-name-certificate', ['openssl', 'x509', '-req', '-in', str(public / 'wrong-name.csr'),
                '-CA', str(public / 'tls.crt'), '-CAkey', str(public / 'tls.key'), '-set_serial', '2',
                '-out', str(public / 'wrong-name.crt'), '-days', '1', '-extfile', str(public / 'wrong-name.ext')])
            (public / 'wrong-name.key').chmod(0o644)
            network = owned.create('network', ['docker', 'network', 'create', '--internal',
                '--label', 'wk-stable-client-run=' + owned.label, 'wk-scl-' + owned.label], checked)
            state_serial = 0
            def configure(generation, mode='normal'):
                nonlocal state_serial
                state_serial += 1
                path = public / 'next-state.json'
                path.write_bytes(canonical({'serial': state_serial, 'generation': generation, 'mode': mode}))
                path.replace(public / 'state.json')
                return state_serial
            configure('A')
            server = create(['docker', 'run', '--platform', 'linux/amd64', '--network', network,
                '--network-alias', 'packages.githubim.com', '--read-only', '--cap-drop', 'ALL',
                '--security-opt', 'no-new-privileges', '--user', '65532:65532',
                '--sysctl', 'net.ipv4.ip_unprivileged_port_start=0', '--pids-limit', '64', '--memory', '256m',
                '--tmpfs', '/tmp:rw,nosuid,nodev,size=64m', '--volume', str(ROOT) + ':/control:ro',
                '--volume', str(public) + ':/public:rw', image, 'python3', '-B',
                '/control/tests/native_stable_clients_drill.py', '--serve', '/public'])
            checked('start-private-HTTPS', ['docker', 'start', server])
            # Bounded readiness is not a test rerun. No externally reachable port is published.
            checked('private-HTTPS-ready', ['docker', 'exec', server, 'python3', '-c',
                'import socket,time\n'
                'for n in range(100):\n'
                ' try:\n  s=socket.create_connection(("127.0.0.1",443),.1);s.close();break\n'
                ' except OSError: time.sleep(.1)\n'
                'else: raise SystemExit("HTTPS readiness deadline")'], timeout=15)
            def requests(number):
                require(not (public / 'server-error').exists(), 'HTTPS server reported a handler failure')
                path = public / 'requests.jsonl'
                require(path.exists() and path.stat().st_size <= MAX_LOG, 'missing or oversized HTTP request evidence')
                return [json.loads(line) for line in path.read_bytes().splitlines() if json.loads(line)['serial'] == number]
            def fetch(url, maximum):
                require(url.startswith(ORIGIN + '/'), 'TEST ONLY fetch escaped the fixed HTTPS origin')
                code = ('import ssl,urllib.request,sys\n'
                    'class NoRedirect(urllib.request.HTTPRedirectHandler):\n'
                    ' def redirect_request(self,*args): return None\n'
                    'op=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect(),'
                    'urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile="/public/tls.crt")))\n'
                    'with op.open(sys.argv[1],timeout=20) as r:\n'
                    ' b=r.read(int(sys.argv[2])+1)\n'
                    ' if len(b)>int(sys.argv[2]): raise SystemExit("response bound")\n'
                    ' sys.stdout.buffer.write(b)')
                return checked('HTTPS-fetch', ['docker', 'exec', server, 'python3', '-c', code, url, str(maximum)],
                               maximum=maximum, timeout=30)
            def client(command, *, cache=None, wrong_ca=False, allow_failure=False):
                installed = any('/acceptance.py:ro' in token for token in command)
                command = list(command)
                if not installed:
                    for index, token in enumerate(command):
                        if token.endswith(':' + validator.APT_CA_BUNDLE_PATH + ':ro'):
                            command[index] = str(public / ('wrong.crt' if wrong_ca else 'tls.crt')) + token[token.index(':'):]
                    additions = ['--network', network, '--volume', str(public / ('wrong.crt' if wrong_ca else 'tls.crt')) + ':/test/ca.crt:ro',
                                 '--env', 'WK_TEST_ONLY_CLIENT=true', '--env', 'WK_TEST_CA_BUNDLE=/test/ca.crt']
                    if cache is not None:
                        cache.mkdir(exist_ok=True); cache.chmod(0o777)
                        apt = 'WK_REPOSITORY_URL=https://packages.githubim.com/apt' in command
                        mounts = cache_mounts('apt' if apt else 'rpm')
                        for name, destination in mounts:
                            directory = cache / name; directory.mkdir(exist_ok=True); directory.chmod(0o777)
                            # Replace an existing tmpfs at this exact destination.
                            index = 0
                            while index < len(command) - 1:
                                if command[index] == '--tmpfs' and command[index + 1].startswith(destination + ':'):
                                    del command[index:index+2]
                                else: index += 1
                            additions += ['--volume', str(directory) + ':' + destination + ':rw']
                    command[2:2] = additions
                identifier = create(command)
                result = run_command(evidence, 'installed-client' if installed else 'download-client',
                                     ['docker', 'start', '--attach', identifier], timeout=650 if installed else 330)
                if result.returncode and not allow_failure:
                    raise RuntimeError('real installed client failed' if installed else 'real download client failed')
                return result
            def record(name, callback, *, negative=False):
                item = {'case': name, 'passed': False, 'expected_rejection': negative}
                evidence['cases'].append(item); evidence['phase'] = name
                value = callback()
                item['passed'] = True
                if value is not None: item['result'] = value
            for generation in ('A', 'B'):
                configure(generation)
                entry = exports['generations'][generation]
                control = fixtures / entry['control']
                def baseline():
                    value = validator.validate_clients(site_root=None, snapshot_path=None, base_url=ORIGIN,
                        apt_public_cert=control / 'keys/apt-stable.asc', rpm_public_cert=control / 'keys/rpm-stable.asc',
                        expected_version=entry['version'], verify_installed_cli=True, channel='stable',
                        transport_inputs=inputs[generation], test_only=True, test_product_sha=PRODUCT_SHA,
                        runner=client, fetcher=fetch)
                    path = args.output_dir / ('clients-' + generation + '.json')
                    path.write_bytes(canonical(value))
                    return facts(path)
                record('baseline-' + generation + '-four-download-and-installed-cli', baseline)
            # Direct download cases validate consistency of a retained A. They never
            # claim the production exact-B / installed-CLI gate passed for those bytes.
            for family, clients in (('apt', validator.APT_CLIENTS), ('rpm', validator.RPM_CLIENTS)):
                for distribution, client_image in clients:
                    cache = scratch / ('cache-' + distribution)
                    cache.mkdir(); cache_roots.append(cache)
                    previous_cache_generation = None
                    def download(generation, mode, *, expected_failure=False, wrong_ca=False, persisted=False):
                        nonlocal previous_cache_generation
                        cache_before = cache_tool(cache, 'arm') if persisted else None
                        number = configure(generation, mode)
                        entry = exports['generations'][generation]
                        site = fixtures / entry['site']
                        bootstrap = json.loads((site / 'bootstrap/manifest.json').read_bytes())['packages'][family]
                        directory = scratch / ('download-' + distribution + '-' + str(number)); directory.mkdir(mode=0o777); directory.chmod(0o777)
                        cert = fixtures / entry['control'] / ('keys/' + family + '-stable.asc')
                        command = validator.stable_client_command(client_image, directory, cert, family,
                                                                  site / bootstrap['repository_path'])
                        result = client(command, cache=cache if persisted else None, wrong_ca=wrong_ca, allow_failure=expected_failure)
                        if expected_failure:
                            text = result.stdout.decode(errors='replace')
                            require(result.returncode not in (0, 124, 125, 126, 127, 137, 143), 'fault did not produce a package-client rejection')
                            require(not re.search(r'Traceback|command not found|Usage:', text), 'fault failed at the harness layer')
                            require(re.search(r'(?i)certificate|SSL|checksum|hash sum|unexpected size|signature|GPG|404|failed to (fetch|download)|cannot (download|prepare)|curl error|transfer|connection|could not resolve', text),
                                    'fault lacks an authentication or transport diagnostic')
                            observed = requests(number)
                            if not wrong_ca and mode != 'wrong-san':
                                require(observed, 'fault did not reach the HTTPS fixture')
                            fault_response = (verify_apt_by_hash_fault(mode, observed)
                                if mode in ('missing-byhash', 'corrupt-byhash') else None)
                            return {'exit_code': result.returncode, 'request_serial': number, 'fault': mode,
                                    'output_sha256': hashlib.sha256(result.stdout).hexdigest(),
                                    'by_hash_fault_response': fault_response}
                        require(result.returncode == 0, 'download client failed')
                        # Indexed payload expected bytes come directly from the exact
                        # candidate source/signing site, not from what was downloaded.
                        pattern = '*.deb' if family == 'apt' else '*.rpm'
                        product_files = [p for p in site.rglob(pattern) if p.name.startswith('wukongim_') or p.name.startswith('wukongim-3.')]
                        hashes = {facts(p)['sha256'] for p in product_files}
                        require(len(hashes) == 1, 'candidate product package identity is ambiguous')
                        value = validator._download_receipt(directory, family, {next(iter(hashes)): [entry['version']]})
                        observed = requests(number)
                        if family == 'apt':
                            require(any(apt_by_hash_path(item['path']) and item['status'] == 200 for item in observed)
                                    or persisted, 'native APT did not use authenticated by-hash')
                        else:
                            prefix = '/snapshots/stable/r' + str(descriptors[generation]['audit_release_id']) + '-'
                            require(any(item['path'] == MIRROR for item in observed), 'native DNF did not request mirrorlist')
                            metadata = [item['path'] for item in observed if '/repodata/' in item['path'] or '/Packages/' in item['path']]
                            require(metadata and all(path.startswith(prefix) for path in metadata), 'DNF escaped its selected immutable snapshot')
                        if mode.endswith('-switch'):
                            expected_barrier = INRELEASE if family == 'apt' else MIRROR
                            require(any(item['path'] == expected_barrier for item in observed), 'missing exact switch request barrier')
                        cache_result = None
                        if persisted:
                            cache_result = cache_evidence(cache_before, cache_tool(cache, 'inventory'),
                                require_reuse=previous_cache_generation == generation)
                            previous_cache_generation = generation
                        return {'download': value, 'requests': observed, 'cache': cache_result}
                    record(distribution + '-A-to-B-request-barrier', lambda: download('A', family + '-switch'))
                    record(distribution + '-cache-A-first', lambda: download('A', 'normal', persisted=True))
                    record(distribution + '-cache-A-repeat', lambda: download('A', 'normal', persisted=True))
                    record(distribution + '-cache-B-target', lambda: download('B', 'normal', persisted=True))
                    modes = ('missing-byhash', 'corrupt-byhash') if family == 'apt' else ('missing-metadata', 'wrong-asc', 'wrong-mirror')
                    for mode in (*modes, 'redirect', 'disconnect'):
                        record(distribution + '-reject-' + mode, lambda mode=mode: download('A', mode, expected_failure=True), negative=True)
                    record(distribution + '-reject-untrusted-TLS', lambda: download('A', 'normal', expected_failure=True, wrong_ca=True), negative=True)
                    record(distribution + '-reject-wrong-SAN', lambda: download('A', 'wrong-san', expected_failure=True), negative=True)
            def conditional_cache():
                # This is a real TLS cache probe, distinct from the package-client
                # persistent-cache cases whose own conditional requests are logged.
                code = ('import json,ssl,urllib.request,urllib.error,sys\n'
                    'class NoRedirect(urllib.request.HTTPRedirectHandler):\n'
                    ' def redirect_request(self,*args): return None\n'
                    'op=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect(),'
                    'urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile="/public/tls.crt")))\n'
                    'h={} if len(sys.argv)<2 else {"If-None-Match":sys.argv[1]}\n'
                    'try:\n'
                    ' with op.open(urllib.request.Request("https://packages.githubim.com/apt/dists/stable/InRelease",headers=h),timeout=10) as r:\n'
                    '  print(json.dumps({"status":r.status,"etag":r.headers["ETag"]}))\n'
                    'except urllib.error.HTTPError as e:\n'
                    ' if e.code!=304: raise\n'
                    ' print(json.dumps({"status":304,"etag":e.headers["ETag"]}))')
                configure('A')
                def get(tag=None):
                    command = ['docker', 'exec', server, 'python3', '-c', code]
                    if tag is not None: command.append(tag)
                    return json.loads(checked('TLS-conditional-cache', command))
                initial = get(); cached = get(initial['etag'])
                configure('B'); changed = get(initial['etag'])
                require(initial['status'] == 200 and cached['status'] == 304 and changed['status'] == 200
                        and changed['etag'] != initial['etag'], 'conditional HTTPS cache mixed generation identity')
                return {'actor': 'TLS cache probe', 'initial': initial, 'cached': cached, 'changed': changed}
            record('conditional-304-and-changed-generation', conditional_cache)
            configure('A')
            def exact_target():
                entry = exports['generations']['B']; control = fixtures / entry['control']
                try:
                    validator.validate_clients(site_root=None, snapshot_path=None, base_url=ORIGIN,
                        apt_public_cert=control / 'keys/apt-stable.asc', rpm_public_cert=control / 'keys/rpm-stable.asc',
                        expected_version=entry['version'], verify_installed_cli=True, channel='stable',
                        transport_inputs=inputs['B'], test_only=True, test_product_sha=PRODUCT_SHA, runner=client, fetcher=fetch)
                except validator.ClientValidationError as error:
                    require('stable public InRelease differs from exact target' in str(error), 'wrong exact-target rejection')
                    return {'reason': str(error)}
                raise RuntimeError('old A incorrectly passed exact B publication target')
            record('reject-exact-B-target-with-A-entrypoint', exact_target, negative=True)
            # Actual installed acceptance must also reject a wrong expected commit.
            entry = exports['generations']['A']; site = fixtures / entry['site']
            package = next(p for p in site.rglob('*.deb') if p.name.startswith('wukongim_'))
            for fault, case in (('wrong-commit', 'reject-actual-installed-source-SHA'),
                                ('old-version', 'reject-actual-installed-old-version'),
                                ('missing-wkcli', 'reject-actual-installed-missing-wkcli')):
                cli_output = scratch / fault; cli_output.mkdir(mode=0o777); cli_output.chmod(0o777)
                def installed_rejection():
                    result = client(installed_negative_command(validator, validator.APT_CLIENTS[0][1],
                        package, cli_output, fixtures / entry['control'] / 'keys/apt-stable.asc', fault),
                        allow_failure=True)
                    reason = (b'missing executable: /usr/bin/wkcli' if fault == 'missing-wkcli' else
                              b'installed binary identity differs from reviewed release')
                    require(result.returncode == 1 and reason in result.stdout,
                            'installed fault failed outside its intended acceptance gate')
                    if fault == 'missing-wkcli':
                        require(b'TEST ONLY removed wkcli after verified installation' in result.stdout,
                                'missing-wkcli fault did not follow a successful real installation')
                    return {'exit_code': result.returncode, 'fault': fault,
                            'output_sha256': hashlib.sha256(result.stdout).hexdigest()}
                record(case, installed_rejection, negative=True)
            require(not (public / 'server-error').exists(), 'HTTPS fixture had an internal error')
            shutil.copyfile(public / 'requests.jsonl', args.output_dir / 'https-requests.jsonl')
            evidence['a_b'] = {'different_archives': True, 'shared_test_public_keys': True,
                               'bootstrap_versions': ['1.1.1', '1.1.2'], 'network_internal': True,
                               'installed_dependencies_use_normal_network': True, 'no_global_atomicity_claim': True}
            evidence['transport'] = {label: {'inputs': facts(inputs[label]), 'plan_sha256': hashlib.sha256(canonical(plans[label])).hexdigest(),
                                            'candidate': descriptors[label]['candidate']} for label in ('A', 'B')}
        except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
            evidence['exercise_failure'] = str(error)[:1024]
            raise
        finally:
            # A second controlled TERM must not interrupt the bounded cleanup.
            # SIGKILL or host loss cannot execute this finally block.
            handlers = {sig: signal.signal(sig, signal.SIG_IGN) for sig in (signal.SIGTERM, signal.SIGINT)}
            try:
                evidence['reader_cleanup'] = owned.cleanup(final=False)
                evidence['cache_cleanup'] = []
                for cache in cache_roots:
                    item = {'cache': cache.name, 'verified': False}
                    evidence['cache_cleanup'].append(item)
                    try:
                        require(evidence['reader_cleanup']['verified'], 'readers were not verifiably stopped before cache removal')
                        item['result'] = cache_tool(cache, 'remove')
                        item['verified'] = item['result'] == {'remaining': []} and not any(cache.iterdir())
                    except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
                        item['failure'] = str(error)[:512]
                evidence['cleanup'] = owned.cleanup()
                if not all(item['verified'] for item in evidence['cache_cleanup']):
                    evidence['cleanup']['verified'] = False
                    evidence['cleanup']['errors'].append('task cache removal was not verified')
                request_log = Path(temporary) / 'https/requests.jsonl'
                if request_log.is_file():
                    if request_log.stat().st_size <= MAX_LOG:
                        shutil.copyfile(request_log, args.output_dir / 'https-requests.jsonl')
                    else:
                        evidence['request_log_error'] = 'request log limit exceeded'
            finally:
                for sig, handler in handlers.items(): signal.signal(sig, handler)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--serve', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--export-fixtures', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--binary-root', type=Path, help=argparse.SUPPRESS)
    parser.add_argument('--output-dir', type=Path)
    parser.add_argument('--product-root', type=Path)
    parser.add_argument('--product-sha', default=PRODUCT_SHA)
    parser.add_argument('--trusted-root', type=Path)
    args = parser.parse_args(argv)
    if args.serve:
        serve(args.serve); return 0
    if args.export_fixtures:
        value = module('tests/native_stable_candidate_drill.py').export_client_generations(
            output_root=args.export_fixtures, binaries_root=args.binary_root,
            trusted_root=args.trusted_root, product_source_sha=args.product_sha)
        (args.export_fixtures / 'export.json').write_bytes(canonical(value)); return 0
    require(args.output_dir is not None and args.output_dir.is_dir()
            and not args.output_dir.is_symlink() and not any(args.output_dir.iterdir()),
            'output directory must exist, be nonlinked and empty')
    evidence = {'schema': 'wukongim/test-only-stable-clients/v1', 'test_only': True,
                'passed': False, 'published': False, 'public_origin_verified': False,
                'source_provenance_verified': False, 'cases': [], 'commands': []}
    owned = OwnedDocker(uuid.uuid4().hex)
    previous = {}
    try:
        require(args.product_sha == PRODUCT_SHA, 'fixed product source SHA is required')
        for sig in (signal.SIGTERM, signal.SIGINT):
            previous[sig] = signal.signal(sig, lambda signum, _: (_ for _ in ()).throw(RuntimeError('interrupted')))
        exercise(args, evidence, owned)
        require(len(evidence['cases']) == 49 and all(item['passed'] for item in evidence['cases']),
                'native case inventory is incomplete')
        evidence['passed'] = True
    except (RuntimeError, OSError, ValueError, subprocess.SubprocessError) as error:
        evidence['failure'] = str(error)[:1024]
    finally:
        evidence['cleanup'] = owned.cleanup()
        evidence['passed'] = evidence['passed'] and evidence['cleanup']['verified']
        for sig, handler in previous.items():
            signal.signal(sig, handler)
        (args.output_dir / 'native-stable-clients.json').write_bytes(canonical(evidence))
    return 0 if evidence['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
