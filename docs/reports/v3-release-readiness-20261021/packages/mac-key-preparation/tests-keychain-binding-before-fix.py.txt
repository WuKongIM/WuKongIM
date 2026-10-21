"""Fake-command tests only; no real GPG, security, or existing secrets are accessed."""
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

SCRIPT = Path(__file__).with_name('prepare_stable_keys.py')


def load():
    spec = importlib.util.spec_from_file_location('operator_keyprep', SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeCommands:
    def __init__(self, results):
        self.results = list(results)
        self.calls = []

    def __call__(self, argv, *, stage, input_bytes=b'', check=True, **kwargs):
        self.calls.append((list(argv), stage, input_bytes, check))
        if not self.results:
            raise AssertionError('unexpected native command')
        code, output = self.results.pop(0)
        result = subprocess.CompletedProcess(argv, code, output, b'NEVER PRINT raw native stderr')
        if check and code:
            raise load().PreparationError(stage + ' failed')
        return result


class OperatorPreparationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.m = load()

    def test_missing_implementation_is_initial_red(self):
        self.assertTrue(SCRIPT.is_file())

    def test_keychain_existing_refuses_without_password_read(self):
        fake = FakeCommands([(0, b'public metadata')])
        with self.assertRaises(self.m.PreparationError):
            self.m.require_keychain_absent(fake, 'WK-Stable-APT', 'operator', '/owned/keychain')
        self.assertEqual(len(fake.calls), 1)
        self.assertNotIn('-w', fake.calls[0][0])

    def test_keychain_unknown_errors_are_not_absence(self):
        for code in (1, 50, 53, 128):
            with self.subTest(code=code):
                with self.assertRaises(self.m.PreparationError):
                    self.m.require_keychain_absent(FakeCommands([(code, b'')]), 's', 'a', '/owned/keychain')

    def test_absence_uses_exact_known_status(self):
        self.m.require_keychain_absent(FakeCommands([(44, b'')]), 's', 'a', '/owned/keychain')

    def test_single_add_line_password_only_in_stdin_and_verify_owned_item(self):
        password = b'a' * 64
        fake = FakeCommands([(0, b''), (0, password + b'\n')])
        self.m.create_keychain_item(fake, 'WK-Stable-APT', 'operator', password, '/owned/keychain')
        self.assertEqual(fake.calls[0][0], ['/usr/bin/security', '-i'])
        line = fake.calls[0][2]
        self.assertEqual(line.count(b'\n'), 1)
        self.assertTrue(line.endswith(b'\n'))
        self.assertIn(password, line)
        for argv, _, _, _ in fake.calls:
            self.assertNotIn(password.decode(), '\0'.join(argv))
            self.assertNotIn('-U', argv)
            self.assertNotIn('-v', argv)
        self.assertIn('-w', fake.calls[1][0])
        self.assertEqual(fake.calls[1][0][-1], '/owned/keychain')
        self.assertTrue(line.endswith(b' /owned/keychain\n'))
        self.assertFalse(fake.results)

    def test_failed_add_never_reads_possibly_preexisting_password(self):
        fake = FakeCommands([(45, b'')])
        with self.assertRaises(self.m.PreparationError):
            self.m.create_keychain_item(fake, 's', 'a', b'b' * 64, '/owned/keychain')
        self.assertEqual(len(fake.calls), 1)

    def test_mismatch_fails_without_secret_in_error(self):
        password = b'c' * 64
        fake = FakeCommands([(0, b''), (0, b'other')])
        with self.assertRaises(self.m.PreparationError) as caught:
            self.m.create_keychain_item(fake, 's', 'a', password, '/owned/keychain')
        self.assertNotIn(password.decode(), str(caught.exception))
        self.assertNotIn('other', str(caught.exception))

    def test_target_is_exclusive_and_existing_data_preserved(self):
        with tempfile.TemporaryDirectory() as t:
            path = Path(t) / 'stable'
            self.m.create_target(path)
            self.assertEqual(path.stat().st_mode & 0o777, 0o700)
            marker = path / 'retained'
            marker.write_text('keep')
            with self.assertRaises(self.m.PreparationError):
                self.m.create_target(path)
            self.assertEqual(marker.read_text(), 'keep')
            link = Path(t) / 'link'
            link.symlink_to(path)
            with self.assertRaises(self.m.PreparationError):
                self.m.create_target(link)

    def test_default_keychain_is_resolved_without_reading_a_password(self):
        fake = FakeCommands([(0, b'  \"/owned/keychain\"\n')])
        self.assertEqual(self.m.resolve_keychain(fake), '/owned/keychain')
        self.assertEqual(fake.calls[0][0], ['/usr/bin/security', 'default-keychain', '-d', 'user'])
        with self.assertRaises(self.m.PreparationError):
            self.m.resolve_keychain(FakeCommands([(0, b'\"/path with spaces\"')]))

    def test_fingerprint_full_and_short_collisions(self):
        safe = ['A' * 40, 'B' * 40, 'C' * 40]
        self.m.require_distinct(safe)
        for values in (safe + [safe[0]], safe + ['D' * 32 + 'A' * 8]):
            with self.assertRaises(self.m.PreparationError):
                self.m.require_distinct(values)

    def test_signature_exact_signer_primary_and_sha256(self):
        primary, sub = 'A' * 40, 'B' * 40
        good = f'[GNUPG:] VALIDSIG {sub} date 123 0 4 0 1 8 00 {primary}\n'.encode()
        self.m.require_valid_signature(good, sub, primary)
        for bad in (good.replace(sub.encode(), b'C' * 40),
                    good.replace(b' 1 8 00 ', b' 1 10 00 '), good + good,
                    good.replace(primary.encode(), b'C' * 40)):
            with self.assertRaises(self.m.PreparationError):
                self.m.require_valid_signature(bad, sub, primary)

    def test_current_export_selector_has_exact_bang(self):
        fake = FakeCommands([(0, b'encrypted TEST ONLY')])
        output = self.m.export_current(fake, Path('/owned/scratch'), 'B' * 40, b'p')
        self.assertEqual(output, b'encrypted TEST ONLY')
        self.assertIn('B' * 40 + '!', fake.calls[0][0])
        self.assertIn('--export-secret-subkeys', fake.calls[0][0])
        self.assertNotIn('p', fake.calls[0][0])

    def test_complete_restore_requires_all_three_local_secrets(self):
        from types import SimpleNamespace
        primary, current, following = 'A' * 40, 'B' * 40, 'C' * 40
        records = [SimpleNamespace(record_type=kind, fingerprint=fpr, secret_marker='+')
                   for kind, fpr in [('sec', primary), ('ssb', current), ('ssb', following)]]
        self.m.require_full_restore(records, [primary, current, following])
        with self.assertRaises(self.m.PreparationError):
            self.m.require_full_restore(records[:2], [primary, current, following])
        records[0].secret_marker = '#'
        with self.assertRaises(self.m.PreparationError):
            self.m.require_full_restore(records, [primary, current, following])

    def test_default_requires_execute_without_native_calls(self):
        with mock.patch.object(self.m, 'prepare', side_effect=AssertionError('must not run')):
            self.assertEqual(self.m.main([]), 2)

    def test_real_command_errors_are_sanitized(self):
        secret = b'f' * 64
        result = subprocess.CompletedProcess(['tool'], 9, secret, secret)
        with mock.patch.object(self.m.subprocess, 'run', return_value=result):
            with self.assertRaises(self.m.PreparationError) as caught:
                self.m.run_command(['tool'], stage='fake stage', input_bytes=secret)
        self.assertNotIn(secret.decode(), str(caught.exception))


if __name__ == '__main__':
    unittest.main()
