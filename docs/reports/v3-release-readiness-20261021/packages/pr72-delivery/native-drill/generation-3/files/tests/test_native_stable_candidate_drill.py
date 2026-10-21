from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'tests/native_stable_candidate_drill.py'


class NativeStableCandidateDrillTests(unittest.TestCase):
    def load(self):
        self.assertTrue(SCRIPT.is_file(), 'missing native stable candidate drill')
        spec = importlib.util.spec_from_file_location('stable_candidate_drill_test', SCRIPT)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def test_help_never_builds_signs_or_installs(self):
        self.load()
        result = subprocess.run([sys.executable, '-B', str(SCRIPT), '--help'], capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        self.assertIn(b'TEST ONLY', result.stdout)
        for flag in (b'--source-sha', b'--trusted-root', b'--output-dir'):
            self.assertIn(flag, result.stdout)

    def test_missing_tool_preserves_public_failure_receipt(self):
        drill = self.load()
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            with mock.patch.object(drill.shutil, 'which', return_value=None), \
                 mock.patch.object(drill.os, 'geteuid', return_value=65532), \
                 mock.patch.object(drill.sys, 'platform', 'linux'):
                status = drill.main(['--output-dir', tmp, '--source-sha', 'a' * 40, '--trusted-root', tmp])
            self.assertEqual(status, 1)
            receipt = json.loads((output / 'native-stable-candidate.json').read_bytes())
            self.assertFalse(receipt['passed'])
            self.assertTrue(receipt['test_only'])
            self.assertTrue(receipt['scratch_removed'])
            self.assertFalse(receipt['production_keys_used'])
            self.assertFalse(receipt['published'])
            self.assertFalse(receipt['source_provenance_verified'])
            self.assertEqual(receipt['source_sha'], 'a' * 40)
            self.assertEqual(receipt['cases'], [])
            self.assertEqual(list(output.iterdir()), [output / 'native-stable-candidate.json'])

    def test_real_scratch_layout_leaves_room_for_agent_socket_names(self):
        drill = self.load()
        real_temporary_directory = tempfile.TemporaryDirectory
        scratch_paths = []
        def temporary_directory(*args, **kwargs):
            result = real_temporary_directory(*args, **kwargs)
            scratch_paths.append(Path(result.name))
            return result
        with real_temporary_directory() as output:
            with mock.patch.object(drill.tempfile, 'TemporaryDirectory', side_effect=temporary_directory), \
                 mock.patch.object(drill.shutil, 'which', return_value=None), \
                 mock.patch.object(drill.os, 'geteuid', return_value=65532), \
                 mock.patch.object(drill.sys, 'platform', 'linux'):
                self.assertEqual(drill.main(['--output-dir', output, '--source-sha', 'a' * 40,
                                             '--trusted-root', output]), 1)
        self.assertEqual(len(scratch_paths), 1)
        # GnuPG also creates browser/extra sockets. 103 bytes is the smaller
        # macOS/Linux pathname budget, excluding the terminating NUL byte.
        socket = scratch_paths[0] / 'private-test-keys/01-STABLE-CANDIDATE-APT/gnupg/S.gpg-agent.browser'
        self.assertLessEqual(len(bytes(socket)), 103)

    def test_source_setup_failure_retains_a_nonsecret_phase(self):
        drill = self.load()
        evidence = {'cases': [], 'commands': []}
        with mock.patch.object(drill, 'fixture_source', side_effect=AssertionError('TEST ONLY secret detail')):
            with self.assertRaises(AssertionError):
                drill.pipeline(Path('/tmp/unused'), Path('/tmp/unused'), evidence, {})
        self.assertEqual(evidence.get('phase'), 'source-fixture')
        self.assertNotIn('secret detail', json.dumps(evidence))

    def test_key_setup_failure_retains_a_nonsecret_phase(self):
        drill = self.load()
        evidence = {'cases': [], 'commands': []}
        with tempfile.TemporaryDirectory() as temporary:
            with mock.patch.object(drill, 'fixture_source', return_value=Path(temporary)), \
                 mock.patch.object(drill, 'load_fixture', return_value=mock.Mock()), \
                 mock.patch.object(drill, 'generate_control', side_effect=AssertionError('TEST ONLY secret detail')):
                with self.assertRaises(AssertionError):
                    drill.pipeline(Path(temporary), Path(temporary), evidence, {})
        self.assertEqual(evidence.get('phase'), 'test-key-generation')
        self.assertNotIn('secret detail', json.dumps(evidence))

    def test_first_failure_cannot_be_overwritten(self):
        drill = self.load()
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'native-stable-candidate.json'
            path.write_text('first failure\n')
            with self.assertRaises(FileExistsError):
                drill.main(['--output-dir', tmp, '--source-sha', 'a' * 40, '--trusted-root', tmp])
            self.assertEqual(path.read_text(), 'first failure\n')

    def test_actual_child_exit_is_retained_without_secret_stderr(self):
        drill = self.load()
        evidence = {'cases': [], 'commands': []}
        secret = b'TEST ONLY secret stdout or stderr'
        child = subprocess.CompletedProcess(['python3'], 17, secret, secret)
        with mock.patch.object(drill.subprocess, 'run', return_value=child):
            with self.assertRaises(RuntimeError):
                drill.invoke(evidence, 'positive-plan', ['python3'], {}, reject=False)
        self.assertEqual(evidence['commands'][0]['exit_code'], 17)
        self.assertNotIn(secret.decode(), json.dumps(evidence))

    def test_expected_rejection_cannot_accept_success(self):
        drill = self.load()
        evidence = {'cases': [], 'commands': []}
        child = subprocess.CompletedProcess(['python3'], 0, b'{}', b'')
        with mock.patch.object(drill.subprocess, 'run', return_value=child):
            with self.assertRaises(RuntimeError):
                drill.invoke(evidence, 'expected-reject', ['python3'], {}, reject=True)
        self.assertFalse(evidence['cases'][-1]['passed'])

    def test_expected_rejection_cannot_accept_a_crashed_child(self):
        drill = self.load()
        evidence = {'cases': [], 'commands': []}
        child = subprocess.CompletedProcess(['python3'], -9, b'', b'')
        with mock.patch.object(drill.subprocess, 'run', return_value=child):
            with self.assertRaises(RuntimeError):
                drill.invoke(evidence, 'expected-reject', ['python3'], {}, reject=True)
        self.assertFalse(evidence['cases'][-1]['passed'])

    def test_rejection_requires_exact_controlled_reason_not_usage_crash_or_other_failure(self):
        drill = self.load()
        expected = b'production package-site verification failed: APT release_gpg identity differs from its signing receipt: dists/stable/Release.gpg\n'
        for code, diagnostic in (
            (2, b'usage: verifier --required INPUT\n' + expected),
            (1, b'Traceback (most recent call last):\n' + expected),
            (1, b'production package-site verification failed: cannot inspect snapshot\n'),
            (1, b'production package-site verification failed: APT Packages.gz differs from Packages\n'),
        ):
            with self.subTest(code=code, diagnostic=diagnostic):
                evidence = {'cases': [], 'commands': []}
                child = subprocess.CompletedProcess(['python3'], code, b'', diagnostic)
                with mock.patch.object(drill.subprocess, 'run', return_value=child):
                    with self.assertRaises(RuntimeError):
                        drill.invoke(evidence, 'reject-next-key-receipt-integrity', ['python3'], {}, reject=True)
                self.assertFalse(evidence['cases'][-1]['passed'])

    def test_controlled_rejection_records_only_declared_category(self):
        drill = self.load()
        evidence = {'cases': [], 'commands': []}
        child = subprocess.CompletedProcess(['python3'], 1, b'',
            b'production package-site verification failed: APT release_gpg identity differs from its signing receipt: dists/stable/Release.gpg\n')
        with mock.patch.object(drill.subprocess, 'run', return_value=child):
            drill.invoke(evidence, 'reject-next-key-receipt-integrity', ['python3'], {}, reject=True)
        self.assertTrue(evidence['cases'][-1]['passed'])
        self.assertEqual(evidence['cases'][-1]['rejection_category'], 'apt-signature-receipt-integrity')
        self.assertNotIn('verification failed:', json.dumps(evidence))

    def test_json_anchor_mutation_preserves_json_and_changes_only_requested_leaf(self):
        drill = self.load()
        original = {'schema': 'fixture', 'assets': [{'sha256': 'a' * 64, 'id': None}], 'other': [1, 2]}
        raw = drill.canonical(original)
        changed = drill.mutate_json_anchor(raw, ('assets', 0, 'sha256'), 'b' * 64)
        self.assertEqual(json.loads(raw), original)
        self.assertEqual(json.loads(changed), {**original, 'assets': [{'sha256': 'b' * 64, 'id': None}]})
        self.assertEqual(changed, drill.canonical(json.loads(changed)))

    def test_signature_receipt_rebinding_changes_only_signature_facts(self):
        drill = self.load()
        original = {'schema': 'receipt', 'key': {'signing_subkey_fingerprint': 'C' * 40},
                    'result': {'release': {'sha256': 'd' * 64},
                               'release_gpg': {'path': 'dists/stable/Release.gpg', 'sha256': 'a' * 64, 'size': 2}}}
        raw = drill.canonical(original)
        changed = json.loads(drill.rebind_apt_signature_receipt(raw, b'actual public signature'))
        self.assertEqual(changed['key'], original['key'])
        self.assertEqual(changed['result']['release'], original['result']['release'])
        self.assertEqual(changed['result']['release_gpg']['path'], 'dists/stable/Release.gpg')
        self.assertEqual(changed['result']['release_gpg']['size'], len(b'actual public signature'))
        self.assertNotEqual(changed['result']['release_gpg']['sha256'], 'a' * 64)
        self.assertEqual(json.loads(raw), original)

    def test_declared_negative_cases_have_bounded_reason_and_layer(self):
        drill = self.load()
        self.assertEqual(set(drill.REJECTIONS), {name for name in drill.CASES if name.startswith('reject-')})
        for category, prefix, reason in drill.REJECTIONS.values():
            self.assertTrue(category)
            self.assertTrue(prefix.endswith(': '))
            self.assertTrue(reason)
        self.assertIn('reject-next-key-crypto', drill.CASES)
        self.assertIn('reject-wrong-family-key-crypto', drill.CASES)
        self.assertNotIn('reject-rpm-open-checksum', drill.CASES)

    def test_workflow_preserves_isolation_pins_and_public_only_upload(self):
        workflow = (ROOT / '.github/workflows/native-package-repository-drill.yml').read_text()
        start = workflow.index('      - name: Compose and independently verify TEST ONLY stable offline candidate')
        end = workflow.index('      - name: Set up fixed GoReleaser toolchain', start)
        step = workflow[start:end]
        self.assertIn(f'(.cases | length) == {len(self.load().CASES)}', step)
        for required in ('--network none', '--read-only', '--cap-drop ALL', '--user 65532:65532',
                         '--security-opt no-new-privileges', '--cpus 1 --memory 512m --pids-limit 128',
                         'manifests/signing-toolchain.json', 'native_stable_candidate_drill.py',
                         '--trusted-root /control/.trusted/wukongim', '--source-sha "$GITHUB_SHA"',
                         'if: always()', 'native-stable-candidate-test-only',
                         'stable-candidate-native/native-stable-candidate.json',
                         'secret_tree_removed_before_verification == true', 'scratch_removed == true'):
            self.assertIn(required, step)
        for forbidden in ('secrets.', 'environment:', 'contents: write', 'pages: write', '--privileged',
                          '--cap-add', '--network host', 'docker.sock'):
            self.assertNotIn(forbidden, step)


if __name__ == '__main__':
    unittest.main()
