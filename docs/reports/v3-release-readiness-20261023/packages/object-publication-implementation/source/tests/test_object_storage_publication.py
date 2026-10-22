"""Fast disabled-control contracts; process/network integration is an explicit suite."""
from __future__ import annotations

import contextlib
import copy
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'scripts/publish-object-storage-delivery.py'


def load(script=SCRIPT):
    spec = importlib.util.spec_from_file_location('publication_contract', script)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def disabled_control():
    return dict(schema='wukongim.object_storage_publication/v1', enabled=False,
                backend='aliyun_oss_cdn', operation=None, channel=None,
                inputs_sha256=None, plan_sha256=None, targets=None,
                uploader=None, activator=None, expected_configuration_sha256=None,
                managed_config_ids=None)


class DisabledPublication(unittest.TestCase):
    def setUp(self):
        self.p = load()

    def test_checked_in_control_is_exactly_disabled_and_unknown(self):
        actual = self.p.read_current_control()
        self.assertEqual(actual, disabled_control())
        self.assertFalse(actual['enabled'])

    def test_schema_backend_extra_fields_and_non_boolean_authority_refused(self):
        for field, value in [('schema', 'unknown'), ('backend', 'pages'),
                             ('verified', True), ('production_enabled', True),
                             ('enabled', 0), ('enabled', 'false'), ('enabled', None)]:
            with self.subTest(field=field, value=value):
                control = disabled_control(); control[field] = value
                with self.assertRaises(ValueError):
                    self.p.validate_current_control(control)

    def test_any_non_null_disabled_binding_refused(self):
        for field in set(disabled_control()) - {'schema', 'backend', 'enabled'}:
            with self.subTest(field=field):
                control = disabled_control(); control[field] = 'saved-authority'
                with self.assertRaises(ValueError):
                    self.p.validate_current_control(control)

    def test_enabled_control_does_not_unlock_unimplemented_production(self):
        control = disabled_control(); control['enabled'] = True
        with self.assertRaisesRegex(ValueError, 'not implemented'):
            self.p.validate_current_control(control)

    def test_control_json_requires_single_keys_bounded_regular_file(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'control.json'
            with self.assertRaises(ValueError):
                self.p.read_control_file(path)
            for raw in [b'{"enabled":false,"enabled":true}', b'[]', b'x' * 65537]:
                with self.subTest(raw_length=len(raw)):
                    path.write_bytes(raw)
                    with self.assertRaises(ValueError):
                        self.p.read_control_file(path)
            target = Path(directory) / 'real.json'
            target.write_text(json.dumps(disabled_control()))
            path.unlink(); path.symlink_to(target)
            with self.assertRaises(ValueError):
                self.p.read_control_file(path)

    def test_both_operations_refuse_before_reading_missing_inputs_or_writing_output(self):
        with tempfile.TemporaryDirectory() as directory:
            for operation in ('upload', 'activate'):
                output = Path(directory) / operation
                stdout, stderr = io.StringIO(), io.StringIO()
                with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                    code = self.p.main(['--operation', operation, '--inputs', str(output / 'missing'),
                                        '--executor-sha', '1' * 40, '--output-dir', str(output)])
                self.assertEqual(code, 2)
                self.assertEqual(stdout.getvalue(), '')
                self.assertIn('disabled', stderr.getvalue())
                self.assertFalse(output.exists())

    def test_cli_has_no_override_for_provenance_control_credentials_or_tls(self):
        for flag in ('--control-root', '--test-only', '--callback', '--previous',
                     '--insecure', '--fixture-ca', '--connect-to', '--enabled'):
            with self.subTest(flag=flag), contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as caught:
                    self.p.main(['--operation', 'activate', '--inputs', 'unused',
                                 '--executor-sha', '1' * 40, '--output-dir', 'unused', flag])
                self.assertEqual(caught.exception.code, 2)

    def test_probe_rejects_unsafe_request_or_resource_before_transport(self):
        facts = dict(size=1, sha256='0' * 64, cache_control='no-store')
        for uri in ('http://evil.test/', '//evil.test/', '/x#fragment', '/x\\y', '/x\r\nX: y', '/x y'):
            with self.subTest(uri=uri), self.assertRaises(ValueError):
                self.p.probe_public(uri=uri, expected=facts)
        for cname in ('127.0.0.1', 'example.test', 'user@x.kunlunaq.com', 'x.kunlunaq.com:443'):
            with self.subTest(cname=cname), self.assertRaises(ValueError):
                self.p.probe_public(uri='/', expected=facts, cname=cname)
        for field, value in [('size', -1), ('size', True), ('size', 2**50),
                             ('sha256', 'unknown'), ('cache_control', 'no-store\n')]:
            bad = copy.deepcopy(facts); bad[field] = value
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                self.p.probe_public(uri='/', expected=bad)

    def test_integration_workflow_explicitly_runs_non_discovered_suite(self):
        workflow = (ROOT / '.github/workflows/object-storage-provider-contract.yml').read_text()
        self.assertIn('tests/provider_sdk/test_object_storage_publication.py --receipt', workflow)
        self.assertIn('test_object_storage_publication.py -v', workflow)
        self.assertIn('manifests/object-storage-publication.json', workflow)
        self.assertIn('/tmp/object-publication-contract-receipt.json', workflow)
        self.assertNotIn('secrets.', workflow)
        self.assertNotIn('id-token:', workflow)
        self.assertFalse((ROOT / 'tests/provider_sdk/__init__.py').exists())


if __name__ == '__main__':
    unittest.main()
