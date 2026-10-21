"""Lightweight boundaries for the opt-in native stable client laboratory."""
import importlib.util
import copy
from contextlib import ExitStack
import errno
import email.utils
import http.client
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import ssl
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'tests/native_stable_clients_drill.py'


def load():
    spec = importlib.util.spec_from_file_location('native_stable_clients_test', SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class NativeStableClientsTests(unittest.TestCase):
    def test_https_normal_response_has_authenticated_tls_eof(self):
        """Exercise the actual fixture server with strict OpenSSL EOF handling."""
        drill = load()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                '-keyout', str(root / 'tls.key'), '-out', str(root / 'tls.crt'),
                '-days', '1', '-subj', '/CN=packages.githubim.com',
                '-addext', 'subjectAltName=DNS:packages.githubim.com'],
                check=True, capture_output=True, timeout=10)
            # This case only needs the normal SNI context; both files remain TEST ONLY.
            (root / 'wrong-name.crt').write_bytes((root / 'tls.crt').read_bytes())
            (root / 'wrong-name.key').write_bytes((root / 'tls.key').read_bytes())
            body = b'TEST ONLY complete signed-metadata transport bytes\n'
            (root / 'body').write_bytes(body)
            (root / 'routes.json').write_text(json.dumps({'A': {drill.INRELEASE: {'file': 'body'}}, 'B': {}}))
            (root / 'state.json').write_text(json.dumps({'serial': 1, 'generation': 'A', 'mode': 'normal'}))
            ready, servers, failures = threading.Event(), [], []
            class LoopbackServer(drill.ThreadingHTTPServer):
                def __init__(self, address, handler):
                    super().__init__(('127.0.0.1', 0), handler)
                    servers.append(self)
                def serve_forever(self):
                    ready.set()
                    super().serve_forever(poll_interval=0.01)
            def serve():
                try:
                    drill.serve(root)
                except BaseException as error:
                    failures.append(error)
                    ready.set()
            with mock.patch.object(drill, 'ThreadingHTTPServer', LoopbackServer):
                thread = threading.Thread(target=serve, daemon=True)
                thread.start()
                try:
                    self.assertTrue(ready.wait(5), 'TEST ONLY server did not become ready')
                    self.assertEqual(failures, [])
                    context = ssl.create_default_context(cafile=str(root / 'tls.crt'))
                    with socket.create_connection(servers[0].server_address, timeout=5) as raw:
                        with context.wrap_socket(raw, server_hostname='packages.githubim.com',
                                                 suppress_ragged_eofs=False) as connection:
                            connection.sendall(b'GET /apt/dists/stable/InRelease HTTP/1.0\r\nHost: packages.githubim.com\r\n\r\n')
                            response = bytearray()
                            eof_error = None
                            try:
                                while chunk := connection.recv(4096):
                                    response.extend(chunk)
                                    self.assertLess(len(response), 65536)
                            except ssl.SSLEOFError as error:
                                eof_error = error
                            headers, actual = bytes(response).split(b'\r\n\r\n', 1)
                            self.assertIn(b'200 OK', headers)
                            self.assertIn(('Content-Length: ' + str(len(body))).encode(), headers)
                            self.assertEqual(actual, body)
                            self.assertIsNone(eof_error, 'strict TLS EOF after complete valid HTTP response: ' + str(eof_error))
                            connection.unwrap()
                    # A protocol-correct close must not weaken certificate
                    # verification or turn the deliberate short body into success.
                    for client_context, hostname in ((context, 'wrong-name.invalid'),
                                                     (ssl.create_default_context(), 'packages.githubim.com')):
                        with socket.create_connection(servers[0].server_address, timeout=5) as raw:
                            with self.assertRaises(ssl.SSLCertVerificationError):
                                client_context.wrap_socket(raw, server_hostname=hostname)
                    (root / 'state.json').write_text(json.dumps({'serial': 2, 'generation': 'A', 'mode': 'disconnect'}))
                    with socket.create_connection(servers[0].server_address, timeout=5) as raw:
                        with context.wrap_socket(raw, server_hostname='packages.githubim.com',
                                                 suppress_ragged_eofs=False) as connection:
                            connection.sendall(b'GET /apt/dists/stable/InRelease HTTP/1.0\r\nHost: packages.githubim.com\r\n\r\n')
                            response = http.client.HTTPResponse(connection)
                            response.begin()
                            with self.assertRaises(http.client.IncompleteRead) as rejected:
                                response.read()
                            self.assertEqual(rejected.exception.partial, body)
                            self.assertEqual(rejected.exception.expected, 7)
                            response.close()
                            connection.unwrap()
                    self.assertFalse((root / 'server-error').exists())
                finally:
                    if servers and thread.is_alive():
                        servers[0].shutdown()
                    thread.join(5)
                    self.assertFalse(thread.is_alive(), 'TEST ONLY server leaked')
                self.assertEqual(failures, [])

    def test_native_ci_keeps_independent_fixed_source_read_only_job(self):
        workflow = (ROOT / '.github/workflows/native-package-repository-drill.yml').read_text()
        self.assertIn('\n  stable-clients:\n', workflow)
        job = workflow.split('\n  stable-clients:\n', 1)[1]
        self.assertIn('timeout-minutes: 60', job)
        self.assertIn('ref: fc18221282f289088ec9b4282fda8e0bfc67a3b8', job)
        self.assertIn('go-version: "1.25.11"', job)
        self.assertIn('gnupg gpgv rpm dpkg zstd', job)
        self.assertIn('python3 -B tests/native_stable_clients_drill.py', job)
        self.assertIn('if: always()', job)
        self.assertIn('(.cases | length) == 49', job)
        self.assertNotIn('secrets.', job)
        self.assertNotIn('environment:', job)
        self.assertNotIn('contents: write', job)
        self.assertNotIn('/var/run/docker.sock', job)

    def test_ambiguous_create_discovers_only_owned_container_and_network(self):
        drill = load()
        for kind in ('container', 'network'):
            with self.subTest(kind=kind):
                label = '0123456789abcdef0123456789abcdef'
                name = 'wk-scl-' + label + ('-1' if kind == 'container' else '')
                identifier, sentinel = 'a' * 64, 'f' * 64
                resources = {identifier: {'kind': kind, 'name': name, 'label': label},
                             sentinel: {'kind': kind, 'name': 'outside-sentinel', 'label': 'other'}}
                calls = []
                def docker(command, **kwargs):
                    calls.append(command)
                    if command[1:3] == ['rm', '--force'] or command[1:3] == ['network', 'rm']:
                        self.assertNotEqual(command[-1], sentinel)
                        resources.pop(command[-1])
                        return subprocess.CompletedProcess(command, 0, b'', b'')
                    if 'inspect' in command:
                        item = resources[command[-1]]
                        raw = f"{command[-1]} /{item['name']} {item['label']}\n".encode()
                    else:
                        self.assertIn('label=wk-stable-client-run=' + label, command)
                        listed = 'container' if command[1] == 'ps' else 'network'
                        raw = ''.join(key + '\n' for key, value in resources.items()
                                      if value['kind'] == listed and value['label'] == label).encode()
                    return subprocess.CompletedProcess(command, 0, raw, b'')
                owned = drill.OwnedDocker(label, run=docker)
                command = (['docker', 'create', '--name', name, '--label', 'wk-stable-client-run=' + label, 'image']
                           if kind == 'container' else ['docker', 'network', 'create', '--internal', '--label',
                                                        'wk-stable-client-run=' + label, name])
                def interrupted(stage, command):
                    self.assertIn(name, owned.attempts[kind + 's'])
                    raise RuntimeError('TERM after daemon create before ID capture')
                with self.assertRaisesRegex(RuntimeError, 'TERM after daemon'):
                    owned.create(kind, command, interrupted)
                receipt = owned.cleanup()
                self.assertTrue(receipt['verified'], receipt)
                self.assertEqual(set(resources), {sentinel})
                self.assertEqual(receipt['remaining'], {'containers': [], 'networks': []})
                self.assertGreaterEqual(sum('label=wk-stable-client-run=' + label in cmd for cmd in calls), 4)

    def test_uncertain_create_with_failed_inventory_cannot_claim_verified(self):
        drill = load()
        owned = drill.OwnedDocker('task', run=lambda command, **kw:
                                  subprocess.CompletedProcess(command, 1, b'', b'query failed'))
        with self.assertRaisesRegex(RuntimeError, 'interrupted'):
            owned.create('network', ['docker', 'network', 'create', '--label',
                         'wk-stable-client-run=task', 'wk-scl-task'],
                         lambda *args: (_ for _ in ()).throw(RuntimeError('interrupted')))
        self.assertFalse(owned.cleanup()['verified'])

    def test_cache_covers_early_installroot_and_final_cli_paths_without_mixing_state(self):
        drill = load()
        self.assertEqual(drill.cache_mounts('rpm'), [('dnf-cache', '/tmp/client-root/tmp/dnf-cache'),
                                                   ('dnf-state', '/tmp/client-root/tmp/dnf-state'),
                                                   ('dnf-cli-cache', '/tmp/dnf-cache'),
                                                   ('dnf-cli-state', '/tmp/dnf-state')])
        self.assertEqual(drill.cache_mounts('apt'), [('apt', '/var/lib/apt'), ('apt-cache', '/var/cache/apt')])
        with self.assertRaisesRegex(RuntimeError, 'cache family'):
            drill.cache_mounts('other')

    def test_cache_command_composes_real_clients_without_changing_native_parameters(self):
        drill = load()
        validator = drill.module('scripts/validate-production-package-clients.py')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); certificate = root / 'public.asc'; certificate.write_bytes(b'public certificate')
            bootstrap = root / 'bootstrap'; bootstrap.write_bytes(b'bootstrap input')
            for family in ('apt', 'rpm'):
                with self.subTest(family=family), mock.patch.object(validator, '_host_ca_bundle', return_value=certificate):
                    command = validator.stable_client_command('fixed-image', root, certificate, family, bootstrap)
                    destinations = [destination for _, destination in drill.cache_mounts(family)]
                    command[2:2] = ['--tmpfs', destinations[-1] + ':rw,size=1m',
                                    '--tmpfs', destinations[-1] + '-other:rw,size=1m']
                    original = list(command)
                    cache = root / ('cache-' + family)
                    value = drill.cache_client_command(command, cache, family)
                    self.assertEqual(command, original)
                    self.assertFalse(cache.exists(), 'command assembly must not touch host files')
                    self.assertEqual(value[-4:], original[-4:])
                    self.assertEqual(value[:2], ['docker', 'run'])
                    wanted = [str(cache / name) + ':' + destination + ':rw'
                              for name, destination in drill.cache_mounts(family)]
                    volumes = [value[i + 1] for i, token in enumerate(value[:-1]) if token == '--volume']
                    self.assertEqual([item for item in volumes if item.startswith(str(cache) + '/')], wanted)
                    self.assertEqual(len({item.split(':')[0] for item in wanted}), len(wanted))
                    self.assertNotIn('/var/run/docker.sock', str(value))
                    self.assertIn(destinations[-1] + '-other:rw,size=1m', value)
                    kept = []; index = 0
                    while index < len(original):
                        if (original[index] == '--tmpfs'
                                and original[index + 1].split(':', 1)[0] in destinations):
                            index += 2
                        else:
                            kept.append(original[index]); index += 1
                    without_new_binds = []; index = 0
                    while index < len(value):
                        if value[index] == '--volume' and value[index + 1] in wanted:
                            index += 2
                        else:
                            without_new_binds.append(value[index]); index += 1
                    self.assertEqual(without_new_binds, kept)
            with self.assertRaisesRegex(RuntimeError, 'Docker run'):
                drill.cache_client_command(['docker', 'exec'], root, 'rpm')

    def test_cache_metadata_accepts_only_native_forms_under_the_two_cache_roots(self):
        drill = load()
        accepted = [prefix + suffix for prefix in ('dnf-cache/', 'dnf-cli-cache/')
                    for suffix in ('repo/repodata/primary.xml.gz', 'repo.solv', 'repo-filelists.solvx')]
        rejected = ['dnf-cache/expired_repos.json', 'dnf-cli-cache/expired_repos.json',
                    'dnf-cli-cache/packages/product.rpm', 'dnf-cli-cache/log.txt',
                    'dnf-state/repo/repodata/primary.xml.gz', 'dnf-cli-state/repo.solv',
                    'dnf-cli-cache-other/repo.solv', 'apt/lists/partial/Packages']
        for name in accepted + rejected:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp); path = root / name
                path.parent.mkdir(parents=True); path.write_bytes(b'nonempty candidate metadata')
                after = drill.cache_inventory(root)
                if name in accepted:
                    self.assertFalse(drill.cache_evidence({}, after, require_reuse=False)['reuse_required'])
                else:
                    with self.assertRaisesRegex(RuntimeError, 'nonempty metadata'):
                        drill.cache_evidence({}, after, require_reuse=False)
        with self.assertRaisesRegex(RuntimeError, 'nonempty metadata'):
            drill.cache_evidence({}, {}, require_reuse=False)

    def test_cache_proves_actual_reads_and_rejects_replaced_or_unread_metadata(self):
        drill = load()
        for prefix in ('dnf-cache', 'dnf-cli-cache'):
            with self.subTest(prefix=prefix), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp); name = prefix + '/repo/repodata/primary.xml.gz'; path = root / name
                path.parent.mkdir(parents=True); path.write_bytes(b'existing authenticated metadata')
                before = drill.cache_inventory(root, arm=True)
                # Merely observing the same file a second time is not reuse.
                after = drill.cache_inventory(root)
                with self.assertRaisesRegex(RuntimeError, 'read of existing metadata'):
                    drill.cache_evidence(before, after, require_reuse=True)
                before = drill.cache_inventory(root, arm=True)
                path.read_bytes()  # Actual OS read, not a mocked timestamp.
                after = drill.cache_inventory(root)
                value = drill.cache_evidence(before, after, require_reuse=True)
                self.assertEqual(value['metadata_reads'], [name])
                self.assertEqual(value['before'], before)
                self.assertEqual(value['after'], after)
                for field in ('device', 'inode', 'sha256', 'size', 'atime_ns', 'absent-before', 'removed'):
                    with self.subTest(field=field):
                        earlier, later = copy.deepcopy(before), copy.deepcopy(after)
                        if field == 'absent-before': earlier.clear()
                        elif field == 'removed': later.clear()
                        elif field == 'atime_ns': later[name][field] = 1
                        elif field == 'sha256': later[name][field] = 'f' * 64
                        else: later[name][field] += 1
                        with self.assertRaises(RuntimeError):
                            drill.cache_evidence(earlier, later, require_reuse=True)

    def test_cache_new_roots_keep_inventory_guards_and_owned_removal(self):
        drill = load()
        for mutation in ('symlink', 'hardlink', 'oversized', 'empty'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp); path = root / 'dnf-cli-cache/repo/repodata/primary.xml.gz'
                path.parent.mkdir(parents=True); path.write_bytes(b'metadata')
                if mutation == 'symlink': (path.parent / 'link').symlink_to(path)
                elif mutation == 'hardlink': os.link(path, path.parent / 'link')
                elif mutation == 'oversized':
                    with path.open('r+b') as stream: stream.truncate(64 * 1024 * 1024 + 1)
                else: path.write_bytes(b'')
                if mutation == 'empty':
                    self.assertEqual(drill.cache_inventory(root), {})
                    with self.assertRaisesRegex(RuntimeError, 'nonempty metadata'):
                        drill.cache_evidence({}, drill.cache_inventory(root), require_reuse=False)
                else:
                    with self.assertRaises(RuntimeError): drill.cache_inventory(root)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); cache = root / 'owned'; cache.mkdir()
            sentinel = root / 'sentinel'; sentinel.write_bytes(b'keep')
            for name, _ in drill.cache_mounts('rpm'):
                child = cache / name; child.mkdir(); (child / 'content').write_bytes(b'cache')
            self.assertEqual(drill.cache_operation(cache, 'remove'), {'remaining': []})
            self.assertEqual(list(cache.iterdir()), [])
            self.assertEqual(sentinel.read_bytes(), b'keep')

    def test_installed_negative_commands_preserve_install_and_original_acceptance(self):
        drill = load()
        validator = drill.module('scripts/validate-production-package-clients.py')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); package = root / 'candidate.deb'; package.write_bytes(b'public package')
            cert = root / 'public.asc'; cert.write_bytes(b'public key')
            for fault in ('wrong-commit', 'old-version', 'missing-wkcli'):
                command = drill.installed_negative_command(validator, 'image', package, root, cert, fault)
                script = command[-1]
                self.assertIn('sha256sum --check --strict', script)
                self.assertIn('install -y python3 /candidate.deb', script)
                self.assertEqual(script.count('python3 /acceptance.py --fixtures /fixtures'), 1)
                self.assertIn('600s', command)
                if fault == 'old-version':
                    self.assertIn('WK_EXPECTED_VERSION=3.0.1', command)
                    self.assertIn('WK_EXPECTED_COMMIT=' + drill.PRODUCT_SHA, command)
                elif fault == 'missing-wkcli':
                    self.assertLess(script.index('install -y python3 /candidate.deb'), script.index('rm -- /usr/bin/wkcli'))
                    self.assertLess(script.index('rm -- /usr/bin/wkcli'), script.index('python3 /acceptance.py'))
                    self.assertNotIn('rm -- /usr/bin/wukongim', script)
                    self.assertIn('WK_EXPECTED_VERSION=3.0.0', command)

    def test_cache_observation_and_removal_use_owned_offline_root_helper(self):
        drill = load()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for operation in ('inventory', 'arm', 'remove'):
                command = drill.cache_helper_command('pinned-image', root, operation)
                self.assertEqual(command[:2], ['docker', 'run'])
                self.assertIn('none', command)
                self.assertIn('0:0', command)
                self.assertIn(str(root) + ':/cache:rw', command)
                self.assertIn(str(ROOT) + ':/control:ro', command)
                self.assertNotIn('/var/run/docker.sock', str(command))
                self.assertEqual(command[-1], operation)
                self.assertIn('DAC_OVERRIDE', command)
                self.assertNotIn('CHOWN', command)
            with self.assertRaisesRegex(RuntimeError, 'cache operation'):
                drill.cache_helper_command('pinned-image', root, 'outside')
            # Real filesystem operation proves cleanup removes only this cache,
            # including a symlink itself without following its outside target.
            outside = root / 'outside'; outside.mkdir(); (outside / 'sentinel').write_bytes(b'keep')
            cache = root / 'cache'; cache.mkdir(); (cache / 'nested').mkdir()
            (cache / 'nested/item').write_bytes(b'metadata')
            (cache / 'outside-link').symlink_to(outside)
            self.assertEqual(drill.cache_operation(cache, 'remove'), {'remaining': []})
            self.assertEqual(list(cache.iterdir()), [])
            self.assertEqual((outside / 'sentinel').read_bytes(), b'keep')

    def test_cleanup_stages_cannot_hide_prior_failure_and_fixture_is_host_nonroot(self):
        drill = load()
        with mock.patch.object(drill.os, 'getuid', return_value=1001), mock.patch.object(drill.os, 'getgid', return_value=121):
            self.assertEqual(drill.fixture_user(), '1001:121')
        with mock.patch.object(drill.os, 'getuid', return_value=0):
            with self.assertRaisesRegex(RuntimeError, 'non-root host'):
                drill.fixture_user()
        calls = []
        fail = True
        def docker(command, **kwargs):
            calls.append(command)
            return subprocess.CompletedProcess(command, int(fail), b'', b'unavailable' if fail else b'No such container')
        owned = drill.OwnedDocker('stage', run=docker)
        owned.attempts['containers'].add('wk-scl-stage-1')
        first = owned.cleanup(final=False)
        self.assertFalse(first['verified'])
        fail = False
        final = owned.cleanup()
        self.assertFalse(final['verified'])
        self.assertGreaterEqual(len(calls), 8)
        self.assertTrue(final['errors'])

    def test_two_stage_cleanup_retires_removed_ids_but_keeps_inventory_obligation(self):
        drill = load(); label = 'two-stages'; calls = []
        ids = {'container': 'a' * 64, 'network': 'b' * 64}
        names = {'container': 'wk-scl-two-stages-1', 'network': 'wk-scl-two-stages'}
        resources = {ids[kind]: kind for kind in ids}
        def docker(command, **kwargs):
            calls.append(command)
            if 'inspect' in command:
                kind = resources.get(command[-1])
                if kind is None:
                    diagnostic = (('Error response from daemon: network ' + command[-1] + ' not found')
                                  if command[1] == 'network' else ('Error: No such object: ' + command[-1]))
                    return subprocess.CompletedProcess(command, 1, b'', diagnostic.encode())
                raw = f"{command[-1]} /{names[kind]} {label}\n".encode()
            elif command[1:3] in (['rm', '--force'], ['network', 'rm']):
                resources.pop(command[-1]); raw = b''
            else:
                self.assertIn('label=wk-stable-client-run=' + label, command)
                kind = 'container' if command[1] == 'ps' else 'network'
                raw = ''.join(identifier + '\n' for identifier, found in resources.items() if found == kind).encode()
            return subprocess.CompletedProcess(command, 0, raw, b'')
        owned = drill.OwnedDocker(label, run=docker)
        for kind in ('container', 'network'):
            command = (['docker', 'create', '--name', names[kind], '--label', 'wk-stable-client-run=' + label, 'image']
                       if kind == 'container' else ['docker', 'network', 'create', '--label',
                                                    'wk-stable-client-run=' + label, names[kind]])
            owned.create(kind, command, lambda *args, kind=kind: ids[kind].encode())
        self.assertTrue(owned.cleanup(final=False)['verified'])
        final = owned.cleanup()
        self.assertTrue(final['verified'], final)
        self.assertEqual(final['creation_attempts'], {'containers': [names['container']], 'networks': [names['network']]})
        self.assertEqual(sum('inspect' in command for command in calls), 2)
        self.assertEqual(sum('label=wk-stable-client-run=' + label in command for command in calls), 8)
        self.assertEqual(resources, {})

    def test_command_deadline_output_bound_and_original_exit(self):
        drill = load()
        evidence = {'commands': []}
        result = drill.run_command(evidence, 'ordinary-failure',
                                  [sys.executable, '-c', 'print("public error");raise SystemExit(8)'])
        self.assertEqual(result.returncode, 8)
        self.assertIn(b'public error', result.stdout)
        with self.assertRaisesRegex(RuntimeError, 'output limit'):
            drill.run_command(evidence, 'bounded-output',
                              [sys.executable, '-c', 'print("x"*10000)'], maximum=100)
        with self.assertRaisesRegex(RuntimeError, 'deadline'):
            drill.run_command(evidence, 'bounded-deadline',
                              [sys.executable, '-c', 'import time;time.sleep(10)'], timeout=0.05)
        self.assertEqual(evidence['commands'][0]['exit_code'], 8)
        self.assertFalse(evidence['commands'][0]['timed_out'])
        self.assertTrue(evidence['commands'][-1]['timed_out'])

    def test_help_and_wrong_product_identity_do_not_launch_tools(self):
        self.assertTrue(SCRIPT.is_file(), 'missing stable native client drill')
        result = subprocess.run([sys.executable, '-B', str(SCRIPT), '--help'], capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertIn(b'TEST ONLY', result.stdout)
        drill = load()
        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(drill.subprocess, 'run') as run:
            output = Path(tmp) / 'out'; output.mkdir()
            status = drill.main(['--output-dir', str(output), '--product-root', tmp,
                                 '--product-sha', 'a' * 40, '--trusted-root', tmp])
            self.assertEqual(status, 1)
            run.assert_not_called()
            value = json.loads((output / 'native-stable-clients.json').read_bytes())
            self.assertFalse(value['passed'])
            self.assertEqual(value['failure'], 'fixed product source SHA is required')
            self.assertTrue(value['cleanup']['verified'])

    def test_http_routes_preserve_a_bytes_at_request_barriers(self):
        drill = load()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for name, raw in [('ai', b'A release'), ('bi', b'B release'), ('ah', b'A index'),
                              ('am', b'https://packages.githubim.com/snapshots/stable/r1/repo/\n'),
                              ('bm', b'https://packages.githubim.com/snapshots/stable/r2/repo/\n')]:
                (root/name).write_bytes(raw)
            maps = {'A': {'/apt/dists/stable/InRelease': {'file': 'ai'},
                          '/apt/by-hash/SHA256/a': {'file': 'ah'},
                          '/rpm/stable/el/9/x86_64/mirrorlist': {'file': 'am'}},
                    'B': {'/apt/dists/stable/InRelease': {'file': 'bi'},
                          '/apt/by-hash/SHA256/a': {'file': 'ah'},
                          '/rpm/stable/el/9/x86_64/mirrorlist': {'file': 'bm'}}}
            route = drill.RouteTable(root, maps)
            route.configure({'serial': 1, 'generation': 'A', 'mode': 'apt-switch'})
            self.assertEqual(route.response('/apt/dists/stable/InRelease', {})[1], b'A release')
            self.assertEqual(route.response('/apt/dists/stable/InRelease', {})[1], b'B release')
            self.assertEqual(route.response('/apt/by-hash/SHA256/a', {})[1], b'A index')
            self.assertEqual(route.response('/../ai', {})[0], 404)
            self.assertEqual(route.response('/ai?x=1', {})[0], 404)
            route.configure({'serial': 2, 'generation': 'A', 'mode': 'rpm-switch'})
            self.assertIn(b'/r1/', route.response('/rpm/stable/el/9/x86_64/mirrorlist', {})[1])
            self.assertIn(b'/r2/', route.response('/rpm/stable/el/9/x86_64/mirrorlist', {})[1])
            route.configure({'serial': 3, 'generation': 'A', 'mode': 'normal'})
            status, raw, headers = route.response('/apt/by-hash/SHA256/a', {})
            self.assertEqual(status, 200)
            self.assertEqual(route.response('/apt/by-hash/SHA256/a', {'If-None-Match': headers['ETag']})[0], 304)

    def test_both_apt_hash_algorithms_retain_a_and_inject_real_faults(self):
        drill = load()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for name, raw in (('ai', b'A release'), ('bi', b'B release'),
                              ('ah', b'A index'), ('bh', b'B index')):
                (root / name).write_bytes(raw)
            for algorithm in ('SHA256', 'SHA512'):
                digest = lambda raw: hashlib.new(algorithm.lower(), raw).hexdigest()
                prefix = '/apt/dists/stable/main/binary-amd64/'
                a_path = prefix + 'by-hash/' + algorithm + '/' + digest(b'A index')
                b_path = prefix + 'by-hash/' + algorithm + '/' + digest(b'B index')
                mutable = prefix + 'Packages.gz'
                maps = {'A': {drill.INRELEASE: {'file': 'ai'}, a_path: {'file': 'ah'}, mutable: {'file': 'ah'}},
                        'B': {drill.INRELEASE: {'file': 'bi'}, a_path: {'file': 'ah'},
                              b_path: {'file': 'bh'}, mutable: {'file': 'bh'}}}
                for serial, mode in enumerate(('apt-switch', 'missing-byhash', 'corrupt-byhash'), 1):
                    with self.subTest(algorithm=algorithm, mode=mode):
                        route = drill.RouteTable(root, maps)
                        route.configure({'serial': serial, 'generation': 'A', 'mode': mode})
                        self.assertEqual(route.response(drill.INRELEASE, {})[1], b'A release')
                        self.assertEqual(route.generation, 'B')
                        status, raw, _ = route.response(a_path, {})
                        if mode == 'apt-switch':
                            self.assertEqual((status, raw), (200, b'A index'))
                            self.assertEqual(route.response(b_path, {})[1], b'B index')
                        elif mode == 'missing-byhash':
                            self.assertEqual((status, raw), (404, b''))
                        else:
                            self.assertEqual(status, 200)
                            self.assertNotEqual(digest(raw), digest(b'A index'))
                        # The mutable fallback has B bytes and cannot satisfy the
                        # A release. An unplanned alias must remain a real 404.
                        self.assertEqual(route.response(mutable, {})[1], b'B index')
                        unplanned = prefix + 'by-hash/' + algorithm + '/' + digest(b'not planned')
                        self.assertEqual(route.response(unplanned, {})[0], 404)

    def test_native_apt_proof_recognizes_only_supported_immutable_paths(self):
        drill = load()
        prefix = '/apt/dists/stable/main/binary-amd64/'
        for algorithm in ('SHA256', 'SHA512'):
            digest = hashlib.new(algorithm.lower(), b'index').hexdigest()
            self.assertTrue(drill.apt_by_hash_path(prefix + 'by-hash/' + algorithm + '/' + digest))
        for path in (prefix + 'Packages.gz', prefix + 'Packages',
                     prefix + 'by-hash/SHA1/' + 'a' * 40,
                     prefix + 'by-hash/SHA512/' + 'a' * 64,
                     prefix + 'by-hash/SHA256/' + 'a' * 64 + '?mutable=1'):
            self.assertFalse(drill.apt_by_hash_path(path), path)

    def test_by_hash_rejection_requires_the_actual_fault_response(self):
        drill = load()
        corrupt_sha = hashlib.sha256(b'changed authenticated index\n').hexdigest()
        for algorithm in ('SHA256', 'SHA512'):
            path = '/apt/dists/stable/main/binary-amd64/by-hash/' + algorithm + '/' + hashlib.new(algorithm.lower(), b'A index').hexdigest()
            for mode, status, digest in (('missing-byhash', 404, hashlib.sha256(b'').hexdigest()),
                                         ('corrupt-byhash', 200, corrupt_sha)):
                diagnostic = '404 Not Found' if mode == 'missing-byhash' else 'Hash Sum mismatch'
                valid = {'path': path, 'status': status, 'sha256': digest, 'response_write_completed': True}
                with self.subTest(algorithm=algorithm, mode=mode):
                    self.assertEqual(drill.verify_apt_by_hash_fault(mode, [valid], diagnostic), valid)
                false_receipts = [[], [{'path': drill.INRELEASE, 'status': status, 'sha256': digest}],
                    [dict(valid, path='/apt/dists/stable/main/binary-amd64/Packages.gz')],
                    [dict(valid, status=200 if status == 404 else 404)],
                    [dict(valid, response_write_completed=False)],
                    [{key: value for key, value in valid.items() if key != 'response_write_completed'}]]
                if mode == 'corrupt-byhash':
                    false_receipts.append([dict(valid, sha256=hashlib.sha256(b'A index').hexdigest())])
                for observed in false_receipts:
                    with self.subTest(algorithm=algorithm, mode=mode, observed=observed):
                        with self.assertRaisesRegex(RuntimeError, 'by-hash fault response'):
                            drill.verify_apt_by_hash_fault(mode, observed, diagnostic)

    def test_by_hash_fault_diagnostics_require_apt_status_or_integrity_failure(self):
        drill = load()
        path = '/apt/dists/stable/main/binary-amd64/by-hash/SHA512/' + 'a' * 128
        integrity = ('Hash Sum mismatch', 'File has unexpected size (28 != 605). Mirror sync in progress?', 'Size mismatch')
        for mode in ('missing-byhash', 'corrupt-byhash'):
            receipt = {'path': path, 'status': 404 if mode == 'missing-byhash' else 200,
                       'sha256': hashlib.sha256(drill.CORRUPT_APT_INDEX).hexdigest(), 'response_write_completed': True}
            for text in (*integrity, *(('404 Not Found [IP: 127.0.0.1 443]',) if mode == 'missing-byhash' else ())):
                with self.subTest(mode=mode, accepted=text):
                    self.assertEqual(drill.verify_apt_by_hash_fault(mode, [receipt], text), receipt)
            diagnostics = ['OpenSSL error: unexpected eof while reading', 'SSL connection failed before response',
                           'Failed to fetch authenticated index', 'Could not resolve host', 'GPG signature failure',
                           'checksum could not be read', 'downloaded 4040 bytes before TLS failure']
            if mode == 'corrupt-byhash':
                diagnostics.append('404 Not Found')
            for text in diagnostics:
                with self.subTest(mode=mode, rejected=text):
                    with self.assertRaisesRegex(RuntimeError, 'APT fault diagnostic'):
                        drill.verify_apt_by_hash_fault(mode, [receipt], text)

    def test_handler_fault_receipts_require_successful_write_and_flush(self):
        """Run the actual handler with failures at each transport write boundary."""
        drill = load()
        for mode in ('missing-byhash', 'corrupt-byhash'):
            for failure in ('send_response', 'end_headers', 'write', 'flush', None):
                with self.subTest(mode=mode, failure=failure), tempfile.TemporaryDirectory() as tmp:
                    root = Path(tmp)
                    path = '/apt/dists/stable/main/binary-amd64/by-hash/SHA512/' + 'a' * 128
                    (root / 'body').write_bytes(b'original index')
                    (root / 'routes.json').write_text(json.dumps({'A': {path: {'file': 'body'}}, 'B': {}}))
                    (root / 'state.json').write_text(json.dumps({'serial': 1, 'generation': 'A', 'mode': mode}))
                    calls = []
                    def boundary(name):
                        calls.append(name)
                        if name == failure:
                            raise BrokenPipeError('injected transport failure at ' + name)
                    class Transport:
                        headers = {}
                        def send_response(self, _): boundary('send_response')
                        def send_header(self, *_): pass
                        def end_headers(self): boundary('end_headers')
                    class Writer:
                        def write(self, raw):
                            boundary('write')
                            return len(raw)
                        def flush(self): boundary('flush')
                    transport = Transport(); transport.path = path; transport.wfile = Writer()
                    class Server:
                        def __init__(self, address, handler):
                            self.handler, self.count, self.socket = handler, 0, object()
                            transport.server = self
                        def serve_forever(self): self.handler.do_GET(transport)
                        def server_close(self): pass
                    with mock.patch.object(drill, 'ThreadingHTTPServer', Server), mock.patch.object(drill.ssl, 'SSLContext'):
                        drill.serve(root)
                    observed = [json.loads(line) for line in (root / 'requests.jsonl').read_text().splitlines()]
                    self.assertEqual(len(observed), 1)
                    self.assertIs(observed[0].get('response_write_completed'), failure is None)
                    if failure is None:
                        self.assertEqual(calls, ['send_response', 'end_headers', 'write', 'flush'])
                    else:
                        self.assertEqual(calls[-1], failure)
                        with self.assertRaisesRegex(RuntimeError, 'by-hash fault response'):
                            drill.verify_apt_by_hash_fault(mode, observed, 'Hash Sum mismatch')

    def rpm_request_fixture(self):
        drill = load()
        repo = 'site/rpm/stable/el/9/x86_64/'
        identities = [{'channel': 'stable', 'audit_release_id': number,
                       'candidate': {'snapshot_sha256': marker * 64}} for number, marker in ((4242, 'a'), (4243, 'b'))]
        prefixes = {item['audit_release_id']: f"snapshots/stable/r{item['audit_release_id']}-{item['candidate']['snapshot_sha256']}/" for item in identities}
        plan = {'snapshots': identities, 'snapshot_objects': [], 'immutable_aliases': [], 'entrypoints': []}
        def obj(number, member, raw, immutable=False):
            value = {'object_key': prefixes[number] + member, 'sha256': hashlib.sha256(raw).hexdigest(),
                     'size': len(raw), 'cache_control': 'public, max-age=31536000, immutable' if immutable else 'no-store',
                     'source': {'channel': 'stable', 'audit_release_id': number, 'member': member}}
            plan['snapshot_objects'].append(value)
            return value
        metadata = [obj(4242, repo + 'repodata/' + name, ('A ' + name).encode())
                    for name in ('repomd.xml', 'repomd.xml.asc', 'primary.xml.gz')]
        payload = obj(4242, repo + 'Packages/wukongim_3.0.0_linux_amd64.rpm', b'A signed RPM', True)
        obj(4243, repo + 'Packages/wukongim_3.0.1_linux_amd64.rpm', b'B signed RPM', True)
        mirror = (drill.ORIGIN + '/' + prefixes[4242] + repo + '\n').encode()
        mirror_facts = {'sha256': hashlib.sha256(mirror).hexdigest(), 'size': len(mirror)}
        plan['entrypoints'].append({'public_uri': drill.MIRROR, **mirror_facts})
        def response(path, value):
            return {'path': path, 'status': 200, 'sha256': value['sha256'], 'size': value['size'],
                    'etag': '"' + value['sha256'] + '"', 'response_write_completed': True,
                    'if_none_match': None, 'if_modified_since': None}
        alias = dict(payload, public_uri='/' + prefixes[4243] + payload['source']['member'])
        alias['object_key'] = 'public' + alias['public_uri']
        plan['immutable_aliases'].append(alias)
        observed = [response(drill.MIRROR, mirror_facts),
                    *[response('/' + item['object_key'], item) for item in metadata],
                    response(drill.MIRROR, {'sha256': hashlib.sha256((drill.ORIGIN + '/' + prefixes[4243] + repo + '\n').encode()).hexdigest(), 'size': len(mirror)}),
                    response(alias['public_uri'], alias)]
        return drill, plan, observed, payload, alias

    def test_rpm_switch_accepts_only_planned_exact_retained_payload_bytes(self):
        drill, plan, observed, payload, alias = self.rpm_request_fixture()
        drill.verify_rpm_requests(observed, plan, 4242, switching=True)
        original_path = observed[-1]['path']
        observed[-1]['path'] = '/' + payload['object_key']
        drill.verify_rpm_requests(observed, plan, 4242, switching=True)
        observed[-1]['path'] = original_path
        # An identical signed member in another verified snapshot may own a
        # deduplicated source locator; the semantic identity remains A's bytes.
        duplicate = copy.deepcopy(payload)
        duplicate['source']['audit_release_id'] = 4243
        duplicate['object_key'] = alias['public_uri'].lstrip('/')
        plan['snapshot_objects'].append(duplicate)
        alias['source'] = copy.deepcopy(duplicate['source'])
        drill.verify_rpm_requests(observed, plan, 4242, switching=True)
        plan['immutable_aliases'].clear()
        drill.verify_rpm_requests(observed, plan, 4242, switching=True)

    def test_rpm_switch_rejects_unbound_paths_sources_and_delivery(self):
        for mutation in ('no-alias', 'unknown-source', 'wrong-member', 'source-sha', 'source-size',
                         'alias-sha', 'alias-size', 'alias-channel', 'alias-architecture',
                         'payload-sha', 'payload-size', 'payload-status', 'payload-incomplete',
                         'metadata-prefix', 'metadata-sha', 'metadata-size', 'metadata-status',
                         'metadata-incomplete', 'unknown-metadata', 'missing-metadata', 'missing-payload',
                         'missing-signature', 'missing-mirror', 'wrong-first-mirror', 'wrong-later-mirror',
                         'mirror-status', 'mirror-incomplete', 'alias-object-key', 'alias-destination',
                         'alias-cache', 'payload-family', 'payload-version'):
            with self.subTest(mutation=mutation):
                drill, plan, observed, payload, alias = self.rpm_request_fixture()
                if mutation == 'no-alias': plan['immutable_aliases'].clear()
                elif mutation == 'unknown-source': alias['source'] = dict(alias['source'], audit_release_id=999)
                elif mutation == 'wrong-member': alias['source'] = dict(alias['source'], member='site/apt/pool/other.deb')
                elif mutation.startswith('source-'):
                    # Add an explicitly named retained source which disagrees
                    # with A; matching only the URI/basename must not pass.
                    duplicate = copy.deepcopy(payload); duplicate['source']['audit_release_id'] = 4243
                    duplicate['object_key'] = alias['public_uri'].lstrip('/')
                    duplicate['sha256' if mutation == 'source-sha' else 'size'] = 'f' * 64 if mutation == 'source-sha' else 999
                    plan['snapshot_objects'].append(duplicate); alias['source'] = copy.deepcopy(duplicate['source'])
                elif mutation == 'alias-sha': alias['sha256'] = 'f' * 64
                elif mutation == 'alias-size': alias['size'] += 1
                elif mutation == 'alias-object-key': alias['object_key'] = 'public/wrong/path'
                elif mutation == 'alias-destination':
                    alias['public_uri'] = alias['public_uri'].replace('r4243-', 'r9999-')
                    alias['object_key'] = 'public' + alias['public_uri']; observed[-1]['path'] = alias['public_uri']
                elif mutation == 'alias-cache': alias['cache_control'] = 'no-store'
                elif mutation == 'alias-channel': alias['source'] = dict(alias['source'], channel='preview')
                elif mutation == 'alias-architecture':
                    alias['public_uri'] = alias['public_uri'].replace('x86_64/', 'aarch64/')
                    observed[-1]['path'] = alias['public_uri']
                elif mutation == 'payload-family': observed[-1]['path'] = '/apt/pool/other.deb'
                elif mutation == 'payload-version': observed[-1]['path'] = observed[-1]['path'].replace('3.0.0', '3.0.1')
                elif mutation.startswith('payload-'):
                    field, value = {'payload-sha': ('sha256', 'f' * 64), 'payload-size': ('size', 999),
                        'payload-status': ('status', 404), 'payload-incomplete': ('response_write_completed', False)}[mutation]
                    observed[-1][field] = value
                elif mutation == 'unknown-metadata': observed[1]['path'] += '.unknown'
                elif mutation == 'missing-metadata': observed[:] = [item for item in observed if '/repodata/' not in item['path']]
                elif mutation == 'missing-payload': observed.pop()
                elif mutation == 'metadata-prefix': observed[1]['path'] = observed[1]['path'].replace('r4242-' + 'a'*64, 'r4243-' + 'b'*64)
                elif mutation.startswith('metadata-'):
                    field, value = {'metadata-sha': ('sha256', 'f' * 64), 'metadata-size': ('size', 999),
                        'metadata-status': ('status', 404), 'metadata-incomplete': ('response_write_completed', False)}[mutation]
                    observed[1][field] = value
                elif mutation == 'missing-signature': observed[:] = [item for item in observed if not item['path'].endswith('repomd.xml.asc')]
                elif mutation == 'missing-mirror': observed[:] = [item for item in observed if item['path'] != drill.MIRROR]
                elif mutation == 'wrong-first-mirror': observed[0]['sha256'] = 'f' * 64
                elif mutation == 'wrong-later-mirror': observed[-2]['sha256'] = 'f' * 64
                elif mutation == 'mirror-status': observed[0]['status'] = 404
                elif mutation == 'mirror-incomplete': observed[0]['response_write_completed'] = False
                with self.assertRaises(RuntimeError):
                    drill.verify_rpm_requests(observed, plan, 4242, switching=True)

    def test_rpm_conditional_metadata_binds_exact_object_and_header_precedence(self):
        for condition in ('etag', 'modified', 'wrong-etag', 'wrong-modified', 'wrong-response-etag', 'missing', 'conflicting', 'body', 'incomplete', 'wrong-url'):
            with self.subTest(condition=condition):
                drill, plan, observed, payload, alias = self.rpm_request_fixture()
                item = observed[1]; expected_sha = item['sha256']
                modified = email.utils.formatdate(1700000000 + int(expected_sha[:6], 16), usegmt=True)
                item.update(status=304, size=0, sha256=hashlib.sha256(b'').hexdigest())
                if condition in ('etag', 'wrong-response-etag'): item['if_none_match'] = item['etag']
                elif condition == 'modified': item['if_modified_since'] = modified
                elif condition == 'wrong-etag': item['if_none_match'] = '"other"'
                elif condition == 'wrong-modified': item['if_modified_since'] = 'Wed, 01 Jan 2020 00:00:00 GMT'
                elif condition == 'conflicting': item.update(if_none_match='"other"', if_modified_since=modified)
                if condition == 'wrong-response-etag': item['etag'] = '"other"'
                if condition in ('body', 'incomplete', 'wrong-url'): item['if_none_match'] = item['etag']
                if condition == 'body': item['size'] = 1
                if condition == 'incomplete': item['response_write_completed'] = False
                if condition == 'wrong-url': item['path'] = item['path'].replace('r4242-', 'r4243-')
                if condition in ('etag', 'modified'):
                    drill.verify_rpm_requests(observed, plan, 4242, switching=False)
                else:
                    with self.assertRaises(RuntimeError):
                        drill.verify_rpm_requests(observed, plan, 4242, switching=False)

    def test_fixture_conditional_etag_precedes_modified_since(self):
        drill = load()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); (root / 'body').write_bytes(b'metadata')
            route = drill.RouteTable(root, {'A': {drill.INRELEASE: {'file': 'body'}}, 'B': {}})
            route.configure({'serial': 1, 'generation': 'A', 'mode': 'normal'})
            _, _, headers = route.response(drill.INRELEASE, {})
            self.assertEqual(route.response(drill.INRELEASE, {'If-None-Match': '"other"',
                'If-Modified-Since': headers['Last-Modified']})[0], 200)

    def test_cleanup_only_uses_recorded_ids_and_reports_inspection_errors(self):
        drill = load()
        commands = []
        def fake(command, **kwargs):
            commands.append(command)
            if 'inspect' in command:
                name = 'wk-scl-unique-test-1' if command[-1] == 'a' * 64 else 'wk-scl-unique-test'
                return subprocess.CompletedProcess(command, 0, (command[-1] + ' /' + name + ' unique-test\n').encode(), b'')
            if command[:3] == ['docker', 'ps', '-aq']:
                return subprocess.CompletedProcess(command, 0, b'', b'')
            if command[:3] == ['docker', 'network', 'ls']:
                return subprocess.CompletedProcess(command, 0, b'', b'')
            return subprocess.CompletedProcess(command, 0, b'', b'')
        owned = drill.OwnedDocker('unique-test', run=fake)
        owned.containers.add('a' * 64); owned.network = 'b' * 64
        owned.attempts['containers'].add('wk-scl-unique-test-1')
        owned.attempts['networks'].add('wk-scl-unique-test')
        result = owned.cleanup()
        self.assertTrue(result['verified'])
        self.assertIn(['docker', 'rm', '--force', 'a' * 64], commands)
        self.assertIn(['docker', 'network', 'rm', 'b' * 64], commands)
        self.assertNotIn('prune', str(commands))
        self.assertNotIn('sentinel', str(commands))
        count = len(commands)
        self.assertEqual(owned.cleanup(), result)
        self.assertEqual(len(commands), count)
        owned = drill.OwnedDocker('unique-test', run=lambda *a, **k: subprocess.CompletedProcess(a, 1, b'', b'error'))
        owned.containers.add('c' * 64)
        self.assertFalse(owned.cleanup()['verified'])


def fixture_module(filename):
    spec = importlib.util.spec_from_file_location('client_fixture_' + filename.removesuffix('.py'), ROOT / 'tests' / filename)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value



class CacheInventorySocketTests(unittest.TestCase):
    """Real filesystem boundaries for the pinned DNF public-key IPC exception."""
    PUBRING = 'dnf-cli-cache/wukongim-stable-5702a8a51b8fa885/pubring'
    NAMES = ('S.gpg-agent', 'S.gpg-agent.browser', 'S.gpg-agent.extra',
             'S.gpg-agent.ssh', 'S.scdaemon')

    def bind_socket(self, stack, path, *, live=True):
        path.parent.mkdir(parents=True, exist_ok=True)
        handle = stack.enter_context(socket.socket(socket.AF_UNIX, socket.SOCK_STREAM))
        handle.bind(str(path))
        if not live:
            handle.close()
        self.assertTrue(stat.S_ISSOCK(path.lstat().st_mode))
        return handle

    def test_five_live_and_closed_sockets_remain_untouched_beside_metadata(self):
        drill = load()
        for live in (True, False):
            with self.subTest(live=live), tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp, ExitStack() as stack:
                root = Path(tmp); nodes = []
                for name in self.NAMES:
                    path = root / self.PUBRING / name
                    self.bind_socket(stack, path, live=live); nodes.append((path, path.lstat()))
                metadata = root / 'dnf-cli-cache/repo/repodata/primary.xml.gz'
                metadata.parent.mkdir(parents=True); metadata.write_bytes(b'authenticated metadata')
                before = drill.cache_inventory(root, arm=True)
                self.assertEqual(set(before), {'dnf-cli-cache/repo/repodata/primary.xml.gz'})
                self.assertEqual(before[next(iter(before))]['sha256'], hashlib.sha256(b'authenticated metadata').hexdigest())
                self.assertEqual(metadata.read_bytes(), b'authenticated metadata')
                for path, original in nodes:
                    current = path.lstat()
                    for field in ('st_mode', 'st_nlink', 'st_size', 'st_ino', 'st_mtime_ns', 'st_atime_ns'):
                        self.assertEqual(getattr(current, field), getattr(original, field), (path.name, field))

    def test_known_sockets_alone_cannot_supply_metadata_or_reuse_evidence(self):
        drill = load()
        with tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp, ExitStack() as stack:
            root = Path(tmp)
            for name in self.NAMES:
                self.bind_socket(stack, root / self.PUBRING / name, live=False)
            inventory = drill.cache_inventory(root, arm=True)
            self.assertEqual(inventory, {})
            for reuse in (False, True):
                with self.subTest(reuse=reuse), self.assertRaisesRegex(RuntimeError, 'nonempty metadata'):
                    drill.cache_evidence({}, inventory, require_reuse=reuse)

    def test_known_sockets_do_not_weaken_actual_metadata_read_proof(self):
        drill = load()
        with tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp, ExitStack() as stack:
            root = Path(tmp)
            for name in self.NAMES:
                self.bind_socket(stack, root / self.PUBRING / name)
            name = 'dnf-cli-cache/repo/repodata/primary.xml.gz'; path = root / name
            path.parent.mkdir(parents=True); path.write_bytes(b'existing metadata')
            before = drill.cache_inventory(root, arm=True)
            after = drill.cache_inventory(root)
            with self.assertRaisesRegex(RuntimeError, 'read of existing metadata'):
                drill.cache_evidence(before, after, require_reuse=True)
            before = drill.cache_inventory(root, arm=True)
            path.read_bytes()  # Real OS access after epoch arming, not a forged timestamp.
            after = drill.cache_inventory(root)
            self.assertEqual(drill.cache_evidence(before, after, require_reuse=True)['metadata_reads'], [name])
            for mutation in ('device', 'inode', 'sha256', 'size', 'absent-before', 'removed'):
                with self.subTest(mutation=mutation):
                    earlier, later = copy.deepcopy(before), copy.deepcopy(after)
                    if mutation == 'absent-before': earlier.clear()
                    elif mutation == 'removed': later.clear()
                    elif mutation == 'sha256': later[name][mutation] = 'f' * 64
                    else: later[name][mutation] += 1
                    with self.assertRaises(RuntimeError):
                        drill.cache_evidence(earlier, later, require_reuse=True)

    def test_socket_name_and_parent_allowlist_are_exact(self):
        drill = load()
        parents = ('', 'apt', 'dnf-cache/wukongim-stable-5702a8a51b8fa885/pubring',
                   'dnf-cli-state/wukongim-stable-5702a8a51b8fa885/pubring',
                   'dnf-cli-cache/wukongim-preview-5702a8a51b8fa885/pubring',
                   'dnf-cli-cache/wukongim-stable-aaa08530d5caaef3/pubring',
                   self.PUBRING + '/sub', self.PUBRING + '-other')
        cases = [(parent, name) for parent in parents for name in self.NAMES]
        cases += [(self.PUBRING, name) for name in ('S.gpg-agent.foo', 'S.scdaemon.extra', 'socket')]
        for parent, name in cases:
            with self.subTest(parent=parent, name=name), tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp, ExitStack() as stack:
                root = Path(tmp); path = root / parent / name
                self.bind_socket(stack, path)
                with self.assertRaisesRegex(RuntimeError, 'unsupported file'):
                    drill.cache_inventory(root)
                self.assertTrue(stat.S_ISSOCK(path.lstat().st_mode))

    def test_known_names_reject_fifo_symlink_and_regular_hardlink_without_reading(self):
        drill = load()
        for name in self.NAMES:
            for kind in ('fifo', 'symlink', 'hardlink'):
                with self.subTest(name=name, kind=kind), tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp:
                    outer = Path(tmp); root = outer / 'c'; path = root / self.PUBRING / name
                    path.parent.mkdir(parents=True)
                    sentinel = outer / 'sentinel'; sentinel.write_bytes(b'keep outside data')
                    if kind == 'fifo': os.mkfifo(path)
                    elif kind == 'symlink': path.symlink_to(sentinel)
                    else: os.link(sentinel, path)
                    with mock.patch.object(Path, 'read_bytes', side_effect=AssertionError('unsupported file must not be read')):
                        with self.assertRaisesRegex(RuntimeError, 'cache.*(?:links|unsupported file)'):
                            drill.cache_inventory(root)
                    self.assertEqual(sentinel.read_bytes(), b'keep outside data')
                    self.assertTrue(path.exists())

    def test_real_hardlinked_socket_is_rejected(self):
        drill = load()
        with tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp, ExitStack() as stack:
            root = Path(tmp); path = root / self.PUBRING / 'S.gpg-agent'
            self.bind_socket(stack, path)
            try:
                os.link(path, path.with_name('S.gpg-agent.extra'))
            except OSError as error:
                if error.errno not in (errno.EPERM, errno.ENOTSUP, errno.EOPNOTSUPP): raise
                self.skipTest(f'OS cannot hardlink an AF_UNIX socket: errno={error.errno}; Linux must exercise this case')
            self.assertEqual(path.lstat().st_nlink, 2)
            with self.assertRaisesRegex(RuntimeError, 'unsupported file'):
                drill.cache_inventory(root)

    def test_known_socket_size_and_link_count_still_fail_closed(self):
        drill = load()
        for field, value in ((3, 2), (6, 1), (0, stat.S_IFCHR | 0o600)):
            with self.subTest(stat_field=field), tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp, ExitStack() as stack:
                root = Path(tmp); path = root / self.PUBRING / 'S.scdaemon'
                self.bind_socket(stack, path)
                original = Path.lstat
                values = list(original(path)); values[field] = value
                def altered(candidate, *args, **kwargs):
                    return os.stat_result(values) if candidate == path else original(candidate, *args, **kwargs)
                # Socket byte size cannot be changed portably; this is a filesystem
                # stat boundary probe, separate from the real hardlink case above.
                with mock.patch.object(Path, 'lstat', altered):
                    with self.assertRaisesRegex(RuntimeError, 'unsupported file'):
                        drill.cache_inventory(root)

    def test_known_names_as_regular_files_are_hashed_and_keep_size_bounds(self):
        drill = load()
        for name in self.NAMES:
            with self.subTest(name=name), tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp:
                root = Path(tmp); path = root / self.PUBRING / name
                path.parent.mkdir(parents=True); path.write_bytes(b'ordinary content')
                inventory = drill.cache_inventory(root)
                self.assertEqual(inventory[self.PUBRING + '/' + name]['sha256'], hashlib.sha256(b'ordinary content').hexdigest())
                with path.open('r+b') as stream: stream.truncate(64 * 1024 * 1024 + 1)
                with mock.patch.object(Path, 'read_bytes', side_effect=AssertionError('oversize read')):
                    with self.assertRaisesRegex(RuntimeError, 'unsupported file'):
                        drill.cache_inventory(root)

    def test_rejected_file_diagnostic_is_relative_bounded_and_stat_specific(self):
        drill = load()
        cases = [('odd\nname', kind) for kind in ('fifo', 'symlink', 'hardlink')]
        cases.append((('x' * 100 + '/') * 3 + 'fifo', 'fifo'))
        for relative, kind in cases:
            with self.subTest(relative=relative, kind=kind), tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp:
                root = Path(tmp) / 'c'; path = root / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                sentinel = Path(tmp) / 'sentinel'; sentinel.write_bytes(b'keep outside')
                if kind == 'fifo': os.mkfifo(path)
                elif kind == 'symlink': path.symlink_to(sentinel)
                else: os.link(sentinel, path)
                info = path.lstat()
                with mock.patch.object(Path, 'read_bytes', side_effect=AssertionError('FIFO read')):
                    with self.assertRaises(RuntimeError) as caught: drill.cache_inventory(root)
                message = str(caught.exception)
                self.assertIn('cache', message)
                self.assertIn(repr(relative)[:240], message)
                self.assertIn('mode=' + oct(info.st_mode), message)
                self.assertIn('nlink=' + str(info.st_nlink), message)
                self.assertIn('size=' + str(info.st_size), message)
                self.assertNotIn(str(root), message)
                self.assertNotIn('\n', message)
                self.assertLessEqual(len(message), 400)

    def test_known_sockets_do_not_bypass_regular_count_or_total_size_budget(self):
        drill = load()
        for count, size in ((10001, 1), (9, 64 * 1024 * 1024)):
            with self.subTest(count=count, size=size), tempfile.TemporaryDirectory(dir='/tmp', prefix='cs-') as tmp, ExitStack() as stack:
                root = Path(tmp); sock = root / self.PUBRING / 'S.gpg-agent'
                self.bind_socket(stack, sock, live=False)
                paths = [root / f'z-{number:05}' for number in range(count)]
                file_stat = os.stat_result((stat.S_IFREG | 0o600, 1, 1, 1, 0, 0, size, 1, 1, 1))
                original = Path.lstat
                def lstat(path, *args, **kwargs): return original(path, *args, **kwargs) if path == sock else file_stat
                with mock.patch.object(Path, 'rglob', return_value=iter([sock, *paths])), \
                     mock.patch.object(Path, 'lstat', lstat), \
                     mock.patch.object(Path, 'is_symlink', return_value=False), \
                     mock.patch.object(Path, 'is_dir', return_value=False), \
                     mock.patch.object(Path, 'is_file', side_effect=lambda: True), \
                     mock.patch.object(Path, 'read_bytes', return_value=b'x'):
                    with self.assertRaisesRegex(RuntimeError, 'inventory exceeds bounds'):
                        drill.cache_inventory(root)

class PublicFixtureBoundaryTests(unittest.TestCase):
    def test_export_rejects_unusable_output_without_native_commands(self):
        module = fixture_module('native_stable_candidate_drill.py')
        for mutation in ('missing', 'file', 'nonempty', 'symlink'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp); output = root / 'output'
                if mutation == 'file': output.write_text('existing')
                elif mutation == 'nonempty': output.mkdir(); (output / 'existing').write_text('keep')
                elif mutation == 'symlink':
                    target = root / 'target'; target.mkdir(); output.symlink_to(target)
                with mock.patch.object(module.subprocess, 'run', side_effect=AssertionError('native command must not run')):
                    with self.assertRaisesRegex(RuntimeError, 'output directory'):
                        module.export_client_generations(output_root=output, binaries_root=root,
                            trusted_root=root, product_source_sha='fc18221282f289088ec9b4282fda8e0bfc67a3b8')
                if mutation == 'nonempty': self.assertEqual((output / 'existing').read_text(), 'keep')

    def test_export_rejects_missing_generation_before_native_commands(self):
        module = fixture_module('native_stable_candidate_drill.py')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); output = root / 'output'; output.mkdir()
            with mock.patch.object(module.subprocess, 'run', side_effect=AssertionError('native command must not run')):
                with self.assertRaisesRegex(RuntimeError, 'binary generation'):
                    module.export_client_generations(output_root=output, binaries_root=root,
                        trusted_root=root, product_source_sha='fc18221282f289088ec9b4282fda8e0bfc67a3b8')
            self.assertEqual(list(output.iterdir()), [])

    def test_export_rejects_wrong_source_before_commands_or_output(self):
        module = fixture_module('native_stable_candidate_drill.py')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); output = root / 'output'; output.mkdir()
            with mock.patch.object(module.subprocess, 'run', side_effect=AssertionError('native command must not run')):
                with self.assertRaisesRegex(RuntimeError, 'fixed product source'):
                    module.export_client_generations(output_root=output, binaries_root=root,
                        trusted_root=root, product_source_sha='a' * 40)
            self.assertEqual(list(output.iterdir()), [])

    def test_invalid_payloads_fail_before_native_commands(self):
        module = fixture_module('native_source_metadata_drill.py')
        for mutation in ('missing', 'extra', 'symlink', 'hardlink', 'directory', 'not-executable',
                         'not-elf', 'short-elf', 'wrong-machine', 'oversized'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                inputs = root / 'inputs'; inputs.mkdir()
                payloads = {}
                for name in ('wukongim', 'wkcli'):
                    header = bytearray(64)
                    header[:7] = b'\x7fELF\x02\x01\x01'
                    header[16:20] = b'\x02\x00\x3e\x00'
                    path = inputs / name; path.write_bytes(header); path.chmod(0o755)
                    payloads[name] = path
                original = payloads['wkcli']
                if mutation == 'missing': del payloads['wkcli']
                elif mutation == 'extra': payloads['other'] = original
                elif mutation == 'symlink':
                    link = inputs / 'link'; link.symlink_to(original); payloads['wkcli'] = link
                elif mutation == 'hardlink': os.link(original, inputs / 'link')
                elif mutation == 'directory': payloads['wkcli'] = inputs
                elif mutation == 'not-executable': original.chmod(0o644)
                elif mutation == 'not-elf': original.write_bytes(b'#!/bin/sh\n' + bytes(64))
                elif mutation == 'short-elf': original.write_bytes(b'\x7fELF')
                elif mutation == 'wrong-machine':
                    raw = bytearray(original.read_bytes()); raw[18:20] = b'\xb7\x00'; original.write_bytes(raw)
                elif mutation == 'oversized':
                    with original.open('r+b') as stream: stream.truncate(256 * 1024 * 1024 + 1)
                build = root / 'build'; build.mkdir()
                with mock.patch.object(module.subprocess, 'run', side_effect=AssertionError('native command must not run')):
                    with self.assertRaisesRegex(RuntimeError, 'binary payload'):
                        module.build_packages(build, '3.0.0', '3.0.0', binary_payloads=payloads)
                self.assertEqual(list(build.iterdir()), [])

    def test_real_payloads_are_inputs_to_both_native_package_commands(self):
        module = fixture_module('native_source_metadata_drill.py')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            inputs = root / 'inputs'; inputs.mkdir()
            payloads = {}
            for name, marker in (('wukongim', b'server supplied bytes'), ('wkcli', b'cli supplied bytes')):
                path = inputs / name
                header = bytearray(64)
                header[:7] = b'\x7fELF\x02\x01\x01'
                header[16:20] = b'\x02\x00\x3e\x00'
                path.write_bytes(header + marker); path.chmod(0o755)
                payloads[name] = path
            build = root / 'build'; build.mkdir()
            observations = []
            def external(command, **kwargs):
                observations.append(command[0])
                if command[0] == 'dpkg-deb':
                    for name, original in payloads.items():
                        staged = build / 'deb/usr/bin' / name
                        self.assertEqual(staged.read_bytes(), original.read_bytes())
                        self.assertEqual(staged.stat().st_mode & 0o777, 0o755)
                    self.assertIn('Depends: systemd', (build / 'deb/DEBIAN/control').read_text())
                    Path(command[-1]).write_bytes(b'command boundary DEB result')
                elif command[0] == 'rpmbuild':
                    for name, original in payloads.items():
                        self.assertEqual((build / 'rpmbuild/SOURCES' / name).read_bytes(), original.read_bytes())
                    spec = Path(command[-1]).read_text()
                    self.assertIn('Requires: systemd', spec)
                    self.assertIn('/usr/bin/wukongim', spec)
                    self.assertIn('/usr/bin/wkcli', spec)
                    rpm = build / 'rpmbuild/RPMS/x86_64/fixture.rpm'; rpm.parent.mkdir(parents=True)
                    rpm.write_bytes(b'command boundary RPM result')
                else:
                    self.fail('unexpected external command: ' + repr(command))
                return subprocess.CompletedProcess(command, 0, '', '')
            with mock.patch.object(module.subprocess, 'run', side_effect=external):
                deb, rpm = module.build_packages(build, '3.0.0', '3.0.0', binary_payloads=payloads)
            self.assertEqual(observations, ['dpkg-deb', 'rpmbuild'])
            self.assertTrue(deb.is_file() and rpm.is_file())

if __name__ == '__main__':
    unittest.main()
