from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location('native_local_drill_under_test', ROOT / 'tests/native_local_signing_drill.py')
drill = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(drill)
SIGNER_SPEC = importlib.util.spec_from_file_location('native_drill_real_signer', drill.SIGNER)
signer = importlib.util.module_from_spec(SIGNER_SPEC)
SIGNER_SPEC.loader.exec_module(signer)


class NativeLocalSigningDiagnosticsTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='wk-drill-diagnostic-TEST-ONLY-')
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.key = SimpleNamespace(public_cert=self.root / 'public.asc',
                                   selected_secret_base64=self.root / 'secret.b64',
                                   passphrase_file=self.root / 'passphrase',
                                   primary_fingerprint='A' * 40,
                                   signing_subkey_fingerprint='B' * 40)
        self.key.public_cert.write_bytes(b'TEST ONLY public placeholder')
        self.key.selected_secret_base64.write_bytes(b'TEVST05MWWtleW1hdGVyaWFs')
        self.key.passphrase_file.write_bytes(b'TEST ONLY diagnostic passphrase')

    def rejected(self, stderr):
        child = subprocess.CompletedProcess(['python3'], 1, b'', stderr)
        with mock.patch.object(drill.subprocess, 'run', return_value=child):
            with self.assertRaises(RuntimeError) as caught:
                drill.invoke(self.root, 'rpm-files', 'rpm', 'files', self.key, 'C' * 40,
                             self.root / 'input', [])
        diagnostic = getattr(caught.exception, 'diagnostic', None)
        self.assertIsNotNone(diagnostic, 'failure must retain exact signer case diagnostic')
        return diagnostic

    def test_positive_rejection_retains_case_and_existing_signer_stage(self):
        result = self.rejected(b'package-family signing failed: RPM payload signing: Packages/wukongim.rpm failed\n')
        self.assertEqual(result['case'], 'rpm-files')
        self.assertEqual(result['family'], 'rpm')
        self.assertEqual(result['input_mode'], 'files')
        self.assertEqual(result['exit_code'], 1)
        self.assertIn('RPM payload signing', result['message'])

    def test_recognized_diagnostic_redacts_test_secret_and_passphrase(self):
        result = self.rejected(b'package-family signing failed: ' + self.key.selected_secret_base64.read_bytes()
                               + b' ' + self.key.passphrase_file.read_bytes() + b'\n')
        output = json.dumps(result).encode()
        self.assertNotIn(self.key.selected_secret_base64.read_bytes(), output)
        self.assertNotIn(self.key.passphrase_file.read_bytes(), output)
        self.assertIn(b'[redacted]', output)

    def test_unknown_or_excessive_stderr_is_not_published(self):
        result = self.rejected(b'unknown secret output\x00\xff' + b'x' * 10000)
        self.assertEqual(result['message'], 'no recognized signer diagnostic')
        self.assertLess(len(json.dumps(result)), 4096)
        known = self.rejected(b'package-family signing failed: ' + b'x' * 10000)
        self.assertLessEqual(len(known['message'].encode()), 2048)

    def test_negative_rejection_keeps_original_no_output_contract(self):
        child = subprocess.CompletedProcess(['python3'], 1, b'', b'expected rejection')
        with mock.patch.object(drill.subprocess, 'run', return_value=child):
            output, receipt = drill.invoke(self.root, 'negative', 'apt', 'files', self.key,
                                           'C' * 40, self.root / 'input', [], reject=True)
            self.assertIsNone(receipt)
            self.assertFalse(output.exists())
            output.mkdir()
            with self.assertRaisesRegex(RuntimeError, 'partial output'):
                drill.invoke(self.root, 'negative', 'apt', 'files', self.key,
                             'C' * 40, self.root / 'input', [], reject=True)

    def test_main_retains_failure_in_public_always_written_receipt(self):
        output = self.root / 'evidence'
        output.mkdir()
        failure = drill.SignerInvocationError({'case': 'rpm-files', 'family': 'rpm',
                                               'input_mode': 'files', 'exit_code': 1,
                                               'message': 'no recognized signer diagnostic'})
        version = subprocess.CompletedProcess(['version'], 0, b'TEST ONLY version\n', b'')
        with mock.patch.object(sys, 'argv', ['drill', '--output-dir', str(output)]), \
             mock.patch.object(drill.shutil, 'which', return_value='/TEST-ONLY/tool'), \
             mock.patch.object(drill, 'run', return_value=version), \
             mock.patch.object(drill.fixtures, 'build_packages', side_effect=failure), \
             mock.patch.dict(os.environ, dict(os.environ), clear=True):
            with self.assertRaises(drill.SignerInvocationError):
                drill.main()
        receipt = json.loads((output / 'native-local-signing.json').read_text())
        self.assertFalse(receipt['passed'])
        self.assertEqual(receipt['failure'], failure.diagnostic)
        self.assertEqual(receipt['cases'], [])

    def test_prepared_rpm_allowlists_are_accepted_by_real_signer_loaders(self):
        deb, rpm = self.root / 'fixture.deb', self.root / 'fixture.rpm'
        deb.write_bytes(b'TEST ONLY data-only DEB placeholder')
        rpm.write_bytes(b'TEST ONLY data-only RPM placeholder')
        apt_output = subprocess.CompletedProcess(['APT tool'], 0, b'TEST ONLY APT output\n', b'')
        with mock.patch.object(drill, 'run', return_value=apt_output):
            _, rpm_input, arguments = drill.prepare_inputs(self.root, deb, rpm)
        expected = {drill.PACKAGE: drill.facts(rpm_input / drill.RPM_REPOSITORY / drill.PACKAGE)}
        for name, wanted in (('new', expected), ('signed', {}), ('active', [drill.PACKAGE])):
            with self.subTest(allowlist=name):
                filename = Path(arguments[arguments.index(f'--{name}-rpm-allowlist') + 1])
                actual = (signer.load_active_allowlist(filename) if name == 'active' else
                          signer.load_package_allowlist(filename, f'{name} RPM allowlist'))
                self.assertEqual(actual, wanted)
                self.assertEqual(filename.read_bytes(), signer.canonical_json(json.loads(filename.read_bytes())))

    def test_real_signer_still_rejects_noncanonical_allowlists(self):
        filename = self.root / 'noncanonical.json'
        filename.write_text(json.dumps({'schema': 'wukongim/rpm-package-allowlist/v1', 'packages': []}))
        with self.assertRaisesRegex(signer.FamilySigningError, 'canonical JSON encoding'):
            signer.load_package_allowlist(filename, 'new RPM allowlist')


if __name__ == '__main__':
    unittest.main()
