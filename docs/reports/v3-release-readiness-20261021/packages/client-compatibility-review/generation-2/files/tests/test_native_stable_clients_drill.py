"""Lightweight boundaries for the opt-in native stable client laboratory."""
import importlib.util
import http.client
import hashlib
import json
import os
from pathlib import Path
import socket
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

    def test_cache_uses_installroot_mounts_and_proves_reads_of_existing_metadata(self):
        drill = load()
        self.assertEqual(drill.cache_mounts('rpm'), [('dnf-cache', '/tmp/client-root/tmp/dnf-cache'),
                                                   ('dnf-state', '/tmp/client-root/tmp/dnf-state')])
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with self.assertRaisesRegex(RuntimeError, 'nonempty metadata'):
                drill.cache_evidence({}, {}, require_reuse=False)
            path = root / 'dnf-cache/repo/repodata/primary.xml.gz'
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
            self.assertEqual(value['metadata_reads'], ['dnf-cache/repo/repodata/primary.xml.gz'])
            self.assertEqual(value['before'], before)
            self.assertEqual(value['after'], after)

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
