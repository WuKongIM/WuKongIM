from __future__ import annotations
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(os.environ['FIXTURE_ROOT'])

def load(filename):
    spec = importlib.util.spec_from_file_location('fixture_' + filename.removesuffix('.py'), ROOT / 'tests' / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

class FixtureBoundaryTests(unittest.TestCase):
    def test_first_native_failure_removes_private_tree_and_exports_nothing(self):
        module = load('native_stable_candidate_drill.py')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); output = root / 'output'; output.mkdir()
            binaries = root / 'binaries'; binaries.mkdir()
            for version in ('3.0.0', '3.0.1'):
                directory = binaries / version; directory.mkdir()
                for name in ('wukongim', 'wkcli'):
                    header = bytearray(64); header[:7] = b'\x7fELF\x02\x01\x01'
                    header[16:20] = b'\x02\x00\x3e\x00'
                    path = directory / name; path.write_bytes(header); path.chmod(0o755)
            homes = []
            def external(command, **kwargs):
                if command[0] == 'gpg':
                    homes.append(Path(command[command.index('--homedir') + 1]))
                    raise RuntimeError('intercepted first native signing command')
                if command[0] == 'gpgconf': return subprocess.CompletedProcess(command, 0, b'', b'')
                self.fail('unexpected native command: ' + repr(command))
            previous_environment = dict(os.environ)
            with mock.patch.object(module.subprocess, 'run', side_effect=external), \
                 mock.patch.object(module.shutil, 'which', return_value='/usr/bin/test-boundary-tool'), \
                 mock.patch.object(module.os, 'geteuid', return_value=65532), \
                 mock.patch.object(module.sys, 'platform', 'linux'):
                with self.assertRaisesRegex(RuntimeError, 'intercepted first native signing command'):
                    module.export_client_generations(output_root=output, binaries_root=binaries,
                        trusted_root=Path(__file__).parent / 'trusted',
                        product_source_sha='fc18221282f289088ec9b4282fda8e0bfc67a3b8')
            self.assertEqual(len(homes), 1)
            self.assertFalse(homes[0].parent.parent.exists())
            self.assertEqual(list(output.iterdir()), [])
            self.assertEqual(dict(os.environ), previous_environment)

    def test_export_rejects_unusable_output_without_native_commands(self):
        module = load('native_stable_candidate_drill.py')
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
        module = load('native_stable_candidate_drill.py')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); output = root / 'output'; output.mkdir()
            with mock.patch.object(module.subprocess, 'run', side_effect=AssertionError('native command must not run')):
                with self.assertRaisesRegex(RuntimeError, 'binary generation'):
                    module.export_client_generations(output_root=output, binaries_root=root,
                        trusted_root=root, product_source_sha='fc18221282f289088ec9b4282fda8e0bfc67a3b8')
            self.assertEqual(list(output.iterdir()), [])

    def test_export_rejects_wrong_source_before_commands_or_output(self):
        module = load('native_stable_candidate_drill.py')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); output = root / 'output'; output.mkdir()
            with mock.patch.object(module.subprocess, 'run', side_effect=AssertionError('native command must not run')):
                with self.assertRaisesRegex(RuntimeError, 'fixed product source'):
                    module.export_client_generations(output_root=output, binaries_root=root,
                        trusted_root=root, product_source_sha='a' * 40)
            self.assertEqual(list(output.iterdir()), [])

    def test_invalid_payloads_fail_before_native_commands(self):
        module = load('native_source_metadata_drill.py')
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
        module = load('native_source_metadata_drill.py')
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
    unittest.main(verbosity=2)
