"""Lightweight boundaries for the opt-in native stable client laboratory."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
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
        self.assertNotIn('secrets.', job)
        self.assertNotIn('environment:', job)
        self.assertNotIn('contents: write', job)
        self.assertNotIn('/var/run/docker.sock', job)

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

    def test_cleanup_only_uses_recorded_ids_and_reports_inspection_errors(self):
        drill = load()
        commands = []
        def fake(command, **kwargs):
            commands.append(command)
            if command[:3] == ['docker', 'ps', '-aq']:
                return subprocess.CompletedProcess(command, 0, b'', b'')
            if command[:3] == ['docker', 'network', 'ls']:
                return subprocess.CompletedProcess(command, 0, b'', b'')
            return subprocess.CompletedProcess(command, 0, b'', b'')
        owned = drill.OwnedDocker('unique-test', run=fake)
        owned.containers.add('a' * 64); owned.network = 'b' * 64
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
