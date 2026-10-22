#!/usr/bin/env python3
"""Explicit real-Git/loopback-TLS contracts, without SDK/cloud/provenance success."""
from __future__ import annotations

import argparse
import contextlib
import hashlib
import http.server
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from tests.test_object_storage_publication import load
from tests.test_plan_object_storage_publication import InputSnapshot, canonical

HOST = 'packages.githubim.com'
BODY = b'exact-public-bytes\n'
FACTS = dict(size=len(BODY), sha256=hashlib.sha256(BODY).hexdigest(), cache_control='no-store')


def command(args, cwd=None):
    result = subprocess.run(args, cwd=cwd, check=True, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=20,
                            env={**os.environ, 'PYTHONDONTWRITEBYTECODE': '1'})
    return result.stdout.decode().strip()


class FreshRealGit(unittest.TestCase):
    """The original fresh validator runs actual Git; only remote HTTP is replaced."""
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='wk-publication-git-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        (self.root / 'home').mkdir(); (self.root / 'templates').mkdir()
        isolated = {'PATH': os.environ.get('PATH', os.defpath), 'HOME': str(self.root / 'home'),
                    'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_SYSTEM': os.devnull,
                    'GIT_CONFIG_GLOBAL': os.devnull, 'GIT_TEMPLATE_DIR': str(self.root / 'templates'),
                    'GIT_CONFIG_COUNT': '3', 'GIT_CONFIG_KEY_0': 'core.hooksPath',
                    'GIT_CONFIG_VALUE_0': os.devnull, 'GIT_CONFIG_KEY_1': 'commit.gpgsign',
                    'GIT_CONFIG_VALUE_1': 'false', 'GIT_CONFIG_KEY_2': 'tag.gpgsign',
                    'GIT_CONFIG_VALUE_2': 'false', 'PYTHONDONTWRITEBYTECODE': '1'}
        environment = patch.dict(os.environ, isolated, clear=True)
        environment.start(); self.addCleanup(environment.stop)
        self.repo = self.root / 'executor'; self.repo.mkdir()
        shutil.copytree(ROOT / 'scripts', self.repo / 'scripts', ignore=shutil.ignore_patterns('__pycache__'))
        self.git('init', '--quiet')
        self.git('add', 'scripts')
        self.git('commit', '--quiet', '-m', 'fixture reviewed scripts')
        self.sha = self.git('rev-parse', 'HEAD')
        self.p = load(self.repo / 'scripts/publish-object-storage-delivery.py')
        self.snapshot = InputSnapshot(self.root / 'data')
        self.inputs = self.root / 'inputs.json'
        self.inputs.write_bytes(canonical(dict(schema='wukongim.object_storage_plan_inputs/v1',
                                              channel='preview', current=self.snapshot.descriptor(), retain=[])))
        # These are pre-existing planner fixtures, not native signatures or source provenance.
        plan = self.p.F.D.P.make_plan(self.inputs)
        self.plan_sha = hashlib.sha256(canonical(plan)).hexdigest()
        self.http_requests = []
        def open_http(opener, request, timeout):
            self.http_requests.append(request.full_url)
            if request.full_url != 'https://api.github.com/repos/WuKongIM/packages/git/ref/heads/main':
                raise AssertionError('unexpected remote request')
            return io.BytesIO(canonical(dict(ref='refs/heads/main', object=dict(type='commit', sha='e' * 40))))
        patcher = patch.object(urllib.request.OpenerDirector, 'open', open_http)
        patcher.start(); self.addCleanup(patcher.stop)
        network = patch.object(socket.socket, 'connect', side_effect=AssertionError('external network forbidden'))
        network.start(); self.addCleanup(network.stop)

    def git(self, *args):
        return command(['git', '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                        '-c', 'core.hooksPath=/dev/null', '-c', 'commit.gpgsign=false', *args], self.repo)

    def verify(self, **kwargs):
        return self.p.verify_fresh_inputs(inputs=self.inputs, expected_plan_sha256=self.plan_sha,
                                          executor_sha=kwargs.pop('executor_sha', self.sha), **kwargs)

    def test_clean_executor_reaches_real_fresh_transport_then_refuses_current_main_drift(self):
        self.assertEqual(self.git('status', '--porcelain', '--untracked-files=all'), '')
        with self.assertRaisesRegex(ValueError, 'fresh delivery input authentication failed'):
            self.verify()
        self.assertEqual(self.http_requests, ['https://api.github.com/repos/WuKongIM/packages/git/ref/heads/main'])

    def test_wrong_head_refuses_before_any_http(self):
        with self.assertRaises(ValueError):
            self.verify(executor_sha='d' * 40)
        self.assertEqual(self.http_requests, [])

    def test_actual_tracked_helper_edit_refuses_before_any_http(self):
        with (self.repo / 'scripts/verify-source-attestations.py').open('a') as output:
            output.write('\n# unreviewed fixture edit\n')
        with self.assertRaises(ValueError):
            self.verify()
        self.assertEqual(self.http_requests, [])

    def test_actual_staged_edit_refuses_before_any_http(self):
        (self.repo / 'new-file').write_text('unreviewed')
        self.git('add', 'new-file')
        with self.assertRaises(ValueError):
            self.verify()
        self.assertEqual(self.http_requests, [])

    def test_actual_untracked_file_refuses_before_any_http(self):
        (self.repo / 'untracked').write_text('unreviewed')
        with self.assertRaises(ValueError):
            self.verify()
        self.assertEqual(self.http_requests, [])

    def test_committed_changed_source_is_not_old_executor(self):
        with (self.repo / 'scripts/verify-source-attestations.py').open('a') as output:
            output.write('\n# different committed source\n')
        self.git('add', 'scripts'); self.git('commit', '--quiet', '-m', 'fixture changed source')
        self.assertEqual(self.git('status', '--porcelain'), '')
        self.assertNotEqual(self.git('rev-parse', 'HEAD'), self.sha)
        with self.assertRaises(ValueError):
            self.verify()
        self.assertEqual(self.http_requests, [])

    def test_original_plan_digest_and_test_only_input_refuse_without_http(self):
        self.plan_sha = '0' * 64
        with self.assertRaises(ValueError):
            self.verify()
        value = json.loads(self.inputs.read_bytes()); value['test_only'] = True
        self.inputs.write_bytes(canonical(value))
        with self.assertRaises(ValueError):
            self.verify()
        self.assertEqual(self.http_requests, [])


class ProbeTLS(unittest.TestCase):
    """Actual curl, fixture CA and server-observed Host/SNI; all scratch is owned."""
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix='wk-publication-tls-')
        cls.addClassCleanup(cls.temp.cleanup)
        cls.root = Path(cls.temp.name)
        cls.p = load()
        cls.curl = shutil.which('curl'); cls.openssl = shutil.which('openssl')
        if not cls.curl or not cls.openssl:
            raise RuntimeError('mandatory curl/openssl integration tools unavailable')
        for name in ('ca', 'other-ca'):
            command([cls.openssl, 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                     '-subj', '/CN=TEST ONLY ' + name, '-keyout', str(cls.root / (name + '.key')),
                     '-out', str(cls.root / (name + '.pem'))])
        for name, san in [('valid', HOST), ('wrong', 'wrong.example.invalid')]:
            command([cls.openssl, 'req', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN=TEST ONLY leaf',
                     '-keyout', str(cls.root / (name + '.key')), '-out', str(cls.root / (name + '.csr'))])
            ext = cls.root / (name + '.ext')
            ext.write_text('subjectAltName=DNS:' + san + '\nextendedKeyUsage=serverAuth\n')
            command([cls.openssl, 'x509', '-req', '-in', str(cls.root / (name + '.csr')),
                     '-CA', str(cls.root / 'ca.pem'), '-CAkey', str(cls.root / 'ca.key'),
                     '-set_serial', '1' if name == 'valid' else '2', '-days', '1',
                     '-extfile', str(ext), '-out', str(cls.root / (name + '.pem'))])

    @contextlib.contextmanager
    def server(self, certificate='valid'):
        observed = {'sni': [], 'requests': []}
        stop = threading.Event()
        class Handler(http.server.BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.1'
            def log_message(self, *args):
                pass
            def do_GET(self):
                observed['requests'].append({'host': self.headers.get('Host'), 'uri': self.path})
                if self.path == '/timeout':
                    stop.wait(2); return
                code = 302 if self.path == '/redirect' else 404 if self.path == '/missing' else 200
                self.send_response(code)
                self.send_header('Cache-Control', 'wrong-cache' if self.path == '/cache' else 'no-store')
                self.send_header('Connection', 'close')
                if self.path == '/redirect':
                    self.send_header('Location', 'https://' + HOST + '/followed')
                if self.path == '/header-limit':
                    self.send_header('X-Large', 'x' * 70000)
                if self.path == '/encoding':
                    self.send_header('Content-Encoding', 'gzip')
                self.end_headers()
                try:
                    if self.path == '/stream':
                        while not stop.wait(0.02):
                            self.wfile.write(b'x'); self.wfile.flush()
                    else:
                        body = BODY + b'excess' if self.path == '/oversize' else BODY[:-1] if self.path == '/short' else BODY
                        self.wfile.write(body); self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError, ssl.SSLError):
                    pass
        class Server(http.server.ThreadingHTTPServer):
            daemon_threads = False
            block_on_close = True
        server = Server(('127.0.0.1', 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(self.root / (certificate + '.pem'), self.root / (certificate + '.key'))
        context.set_servername_callback(lambda sock, name, ctx: observed['sni'].append(name))
        server.socket = context.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, kwargs={'poll_interval': 0.02})
        thread.start()
        try:
            yield server.server_port, observed
        finally:
            stop.set(); server.shutdown(); server.server_close(); thread.join(timeout=3)
            self.assertFalse(thread.is_alive(), 'TLS listener thread leaked')
            with socket.socket() as sock:
                sock.settimeout(0.2)
                self.assertNotEqual(sock.connect_ex(('127.0.0.1', server.server_port)), 0, 'listener leaked')

    def probe(self, port, uri='/', expected=None, ca='ca.pem', timeout=2):
        try:
            return self.p.probe_test_only(uri=uri, expected=expected or FACTS, connect_host='127.0.0.1',
                                          connect_port=port, fixture_ca=self.root / ca, timeout=timeout)
        finally:
            processes = command(['ps', '-axo', 'pid=,ppid=,comm='])
            children = [line for line in processes.splitlines()
                        if len(line.split(None, 2)) == 3 and line.split(None, 2)[1] == str(os.getpid())
                        and Path(line.split(None, 2)[2]).name == 'curl']
            self.assertEqual(children, [], 'curl child was not reaped')

    def test_exact_bytes_canonical_host_sni_and_escaped_path_query_are_preserved(self):
        uri = '/snapshots/stable/a%2Fb/../A.rpm?x=%2F&x=2'
        with self.server() as (port, observed):
            result = self.probe(port, uri)
        self.assertEqual(observed, {'sni': [HOST], 'requests': [{'host': HOST, 'uri': uri}]})
        self.assertEqual(result['sha256'], FACTS['sha256'])
        self.assertEqual(result['size'], len(BODY))
        self.assertTrue(result['test_only'])
        self.assertFalse(result['publication_verified'])
        self.assertFalse(result['production_enabled'])
        self.assertFalse(result['public_verified'])

    def test_wrong_san_is_rejected_by_actual_tls(self):
        with self.server('wrong') as (port, observed):
            with self.assertRaises(ValueError): self.probe(port)
        self.assertEqual(observed['requests'], [])
        self.assertEqual(observed['sni'], [HOST])

    def test_untrusted_ca_is_rejected_by_actual_tls(self):
        with self.server() as (port, observed):
            with self.assertRaises(ValueError): self.probe(port, ca='other-ca.pem')
        self.assertEqual(observed['requests'], [])

    def test_redirect_is_not_followed(self):
        with self.server() as (port, observed):
            with self.assertRaises(ValueError): self.probe(port, '/redirect')
        self.assertEqual([x['uri'] for x in observed['requests']], ['/redirect'])

    def test_http_error_is_not_success(self):
        with self.server() as (port, observed):
            with self.assertRaises(ValueError): self.probe(port, '/missing')
        self.assertEqual(len(observed['requests']), 1)

    def test_mismatched_short_oversized_encoded_or_cache_response_fails(self):
        for uri in ('/short', '/oversize', '/encoding', '/cache'):
            with self.subTest(uri=uri), self.server() as (port, observed):
                with self.assertRaises(ValueError): self.probe(port, uri)
                self.assertEqual(len(observed['requests']), 1)
        with self.server() as (port, observed):
            with self.assertRaises(ValueError): self.probe(port, expected={**FACTS, 'sha256': '0' * 64})

    def test_headers_have_a_hard_bound(self):
        with self.server() as (port, observed):
            with self.assertRaises(ValueError): self.probe(port, '/header-limit')
        self.assertEqual(len(observed['requests']), 1)

    def test_timeout_and_streaming_have_total_deadline_and_cleanup(self):
        for uri in ('/timeout', '/stream'):
            with self.subTest(uri=uri), self.server() as (port, observed):
                start = time.monotonic()
                with self.assertRaises(ValueError):
                    self.probe(port, uri, expected={**FACTS, 'size': 4096}, timeout=0.2)
                self.assertLess(time.monotonic() - start, 2)
                self.assertEqual(len(observed['requests']), 1)

    def test_proxy_and_ca_environment_do_not_override_explicit_fixture_transport(self):
        with patch.dict(os.environ, {'https_proxy': 'http://127.0.0.1:1', 'ALL_PROXY': 'http://127.0.0.1:1',
                                      'CURL_CA_BUNDLE': '/absent/untrusted', 'SSL_CERT_FILE': '/absent/untrusted'}):
            with self.server() as (port, observed):
                self.probe(port)
        self.assertEqual(observed['requests'], [{'host': HOST, 'uri': '/'}])

    def test_test_transport_refuses_external_target_and_unbounded_timeout(self):
        for host, timeout in [('example.invalid', 1), ('127.0.0.1', 0), ('127.0.0.1', 60)]:
            with self.subTest(host=host, timeout=timeout), self.assertRaises(ValueError):
                self.p.probe_test_only(uri='/', expected=FACTS, connect_host=host, connect_port=443,
                                       fixture_ca=self.root / 'ca.pem', timeout=timeout)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(); parser.add_argument('--receipt', type=Path, required=True)
    args = parser.parse_args()
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromModule(sys.modules[__name__]))
    successful = result.wasSuccessful() and result.testsRun > 0 and not result.skipped
    value = {'schema': 'wukongim.object_publication_contract/v1', 'tests_run': result.testsRun,
             'failures': len(result.failures), 'errors': len(result.errors), 'skipped': len(result.skipped),
             'successful': successful, 'real_git_and_loopback_tls_only': True,
             'production_enabled': False, 'publication_verified': False, 'cloud_verified': False,
             'native_provenance_verified': False}
    args.receipt.write_text(json.dumps(value, sort_keys=True, indent=2) + '\n')
    sys.exit(0 if successful else 1)
