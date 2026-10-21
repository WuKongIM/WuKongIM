from __future__ import annotations

import importlib.util
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile
from tempfile import TemporaryDirectory
import unittest
from unittest import mock

from test_validate_production_package_clients import Fixture, ROOT, VERSION, validator

SPEC = importlib.util.spec_from_file_location('installed_cli_acceptance', ROOT / 'scripts/validate-installed-wkcli.py')
acceptance = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(acceptance)
FIXTURES = ROOT / 'tests/fixtures/wkcli-acceptance'
IDENTITY = dict(version=VERSION, commit='a' * 40, build_source='release')
CHECKS = ['version', 'help', 'bench_validate', 'db_query', 'migrate_diagnose', 'invalid_inputs', 'read_only_data']


class InstalledCLIContractTest(unittest.TestCase):
    def test_native_fixture_is_a_registered_format_2_generation(self):
        with tarfile.open(FIXTURES / 'native-v3.tar.gz') as archive:
            self.assertIn('DATA-FORMAT.json', archive.getnames())
            metadata = json.load(archive.extractfile('DATA-FORMAT.json'))
        self.assertEqual('wukongim-v3', metadata['format'])
        self.assertEqual(2, metadata['format_version'])
        self.assertEqual(dict(program='wkcli', version='3.0.0-beta.22',
                              commit='e12d13cdc797c53315f703445d5f4a950e064d6b',
                              build_source='release'), metadata['created_by'])

    def test_unregistered_native_fixture_preserves_original_database_bytes(self):
        path = FIXTURES / 'native-v3-unregistered.tar.gz'
        self.assertEqual('6bfc89f1dbe171b2f3a09998709fe9bfb0c406b962ee0f031ca66425b22377eb',
                         hashlib.sha256(path.read_bytes()).hexdigest())
        with tarfile.open(path) as archive:
            self.assertNotIn('DATA-FORMAT.json', archive.getnames())

    def receipt(self):
        return dict(schema='wukongim/installed-cli-acceptance/v1', identity=IDENTITY,
                    fixtures=json.loads((FIXTURES / 'sha256.json').read_text()), checks=CHECKS, verified=True)

    def remote(self, fixture, runner):
        files = fixture.remote_files()
        with mock.patch.object(validator, '_host_ca_bundle', return_value=fixture.apt_certificate):
            return validator.validate_clients(
                site_root=None, snapshot_path=fixture.snapshot, base_url='https://packages.example/',
                apt_public_cert=fixture.apt_certificate, rpm_public_cert=fixture.rpm_certificate,
                expected_version=VERSION, verify_installed_cli=True, runner=runner,
                fetcher=lambda url, maximum: files[url])

    def runner(self, fixture, transform=lambda value: value):
        def execute(command):
            if '/acceptance.py:ro' not in '\n'.join(command):
                fixture.runner(command)
                return
            fixture.commands.append(command)
            output = fixture._mount_source(command, '/evidence')
            (output / 'installed-cli.json').write_text(json.dumps(transform(self.receipt())))
        return execute

    def test_four_installed_clients_follow_verified_public_downloads(self):
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            result = self.remote(fixture, self.runner(fixture))
            self.assertTrue(result['installed_cli_verified'])
            self.assertTrue(result['status_revalidated'])
            installed = [c for c in fixture.commands if '/acceptance.py:ro' in '\n'.join(c)]
            self.assertEqual(4, len(installed))
            for item in result['apt'] + result['rpm']:
                self.assertEqual(self.receipt(), item['installed_cli'])
            for command in installed:
                rendered = '\n'.join(command)
                self.assertIn('WK_EXPECTED_COMMIT=' + 'a' * 40, rendered)
                self.assertIn('--memory\n768m', rendered)
                self.assertIn('--pids-limit\n128', rendered)
                self.assertIn('timeout\n--kill-after=10s\n600s', rendered)
                self.assertIn('--cap-drop\nALL', rendered)
                for forbidden in ['--privileged', 'SYS_ADMIN', 'NET_ADMIN', 'docker.sock', 'GH_TOKEN', '--nogpgcheck']:
                    self.assertNotIn(forbidden, rendered)
                self.assertIn('sha256sum --check --strict', rendered)
                if '/candidate.rpm:ro' in rendered:
                    self.assertIn('localpkg_gpgcheck=1', rendered)

    def test_wrong_commit_missing_check_or_wrong_fixture_fails_closed(self):
        def wrong_commit(value):
            value['identity'] = dict(IDENTITY, commit='b' * 40)
            return value
        def incomplete(value):
            value['checks'] = ['version', 'help']
            return value
        def wrong_fixture(value):
            value['fixtures'] = {}
            return value
        for change in [wrong_commit, incomplete, wrong_fixture]:
            with self.subTest(change=change.__name__), TemporaryDirectory() as temporary:
                fixture = Fixture(Path(temporary))
                with self.assertRaises(validator.ClientValidationError):
                    self.remote(fixture, self.runner(fixture, change))

    def test_a_failed_distribution_prevents_success_receipt(self):
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            good = self.runner(fixture)
            calls = 0
            def runner(command):
                nonlocal calls
                if '/acceptance.py:ro' in '\n'.join(command):
                    calls += 1
                    if calls == 3:
                        raise subprocess.CalledProcessError(1, command)
                good(command)
            with self.assertRaisesRegex(validator.ClientValidationError, 'installed CLI acceptance failed'):
                self.remote(fixture, runner)
            self.assertEqual(3, calls)

    def test_unverified_download_never_reaches_installation(self):
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            # Corrupt every product download, while keeping bootstrap entrypoint checks independent.
            def corrupt(command):
                self.assertNotIn('/acceptance.py:ro', '\n'.join(command))
                fixture.runner(command)
                if ':/repo:ro' not in '\n'.join(command):
                    for p in fixture._mount_source(command, '/downloads').glob('*.deb'):
                        p.write_bytes(b'wrong payload')
            with self.assertRaises(validator.ClientValidationError):
                self.remote(fixture, corrupt)

    def test_installed_gate_rejects_local_or_unbound_inputs_before_running(self):
        with TemporaryDirectory() as temporary:
            fixture = Fixture(Path(temporary))
            for local, snapshot, version in [(True, fixture.snapshot, VERSION),
                                              (False, None, VERSION), (False, fixture.snapshot, None)]:
                runner = mock.Mock()
                with self.assertRaises(validator.ClientValidationError):
                    validator.validate_clients(
                        site_root=fixture.site if local else None, base_url=None if local else 'https://packages.example/',
                        snapshot_path=snapshot, expected_version=version, verify_installed_cli=True,
                        apt_public_cert=fixture.apt_certificate, rpm_public_cert=fixture.rpm_certificate, runner=runner)
                runner.assert_not_called()

    def test_false_success_and_timeout_are_not_accepted(self):
        with TemporaryDirectory() as temporary:
            root = Path(temporary)
            with mock.patch.object(acceptance.subprocess, 'run', return_value=mock.Mock(returncode=0)):
                with self.assertRaisesRegex(acceptance.AcceptanceError, 'expected 1'):
                    acceptance.run(['cli', 'invalid'], root, {}, expected=1)
            with mock.patch.object(acceptance.subprocess, 'run', side_effect=subprocess.TimeoutExpired(['cli'], 30)):
                with self.assertRaisesRegex(acceptance.AcceptanceError, 'exceeded 30'):
                    acceptance.run(['cli'], root, {})

    def test_fixture_extraction_rejects_escape_and_links(self):
        for name, kind in [('../escape', tarfile.REGTYPE), ('link', tarfile.SYMTYPE)]:
            with self.subTest(name=name), TemporaryDirectory() as temporary:
                root = Path(temporary)
                with tarfile.open(root / 'bad.tar', 'w') as tar:
                    member = tarfile.TarInfo(name)
                    member.type = kind
                    member.linkname = '/etc/passwd' if kind == tarfile.SYMTYPE else ''
                    tar.addfile(member, io.BytesIO())
                with self.assertRaises(acceptance.AcceptanceError):
                    acceptance.unpack(root / 'bad.tar', root / 'out')

    def test_installed_identity_is_checked_before_functional_commands(self):
        with TemporaryDirectory() as temporary:
            binary = Path(temporary) / 'cli'
            binary.write_text('placeholder')
            binary.chmod(0o700)
            for field, bad in [('version', '0.0.0-wrong'), ('commit', 'b' * 40), ('build_source', 'source')]:
                execute = mock.Mock(return_value=json.dumps(dict(IDENTITY, **{field: bad})).encode())
                with self.subTest(field=field), self.assertRaisesRegex(acceptance.AcceptanceError, 'identity differs'):
                    acceptance.validate(binary, binary, VERSION, 'a' * 40, FIXTURES, execute)
                self.assertEqual(1, execute.call_count)

    def test_root_mounted_script_accepts_explicit_fixture_path(self):
        argv = ['/acceptance.py', '--fixtures', str(FIXTURES), '--version', VERSION,
                '--commit', 'a' * 40]
        with mock.patch.object(acceptance, '__file__', '/acceptance.py'), \
             mock.patch.object(acceptance.sys, 'argv', argv), \
             mock.patch.object(acceptance, 'validate', return_value=self.receipt()) as validate, \
             mock.patch('sys.stdout', new=io.StringIO()):
            self.assertEqual(0, acceptance.main())
            self.assertEqual(FIXTURES.resolve(), validate.call_args.args[-1])

    def test_publication_and_repeat_workflows_keep_the_gate_and_read_only_boundary(self):
        publish = (ROOT / '.github/workflows/native-package-publish.yml').read_text()
        public = publish.split('      - name: Validate public downloads and installed CLI acceptance', 1)[1]
        self.assertIn('if [[ "$OPERATION" == add_release ]]; then\n            client_target_args+=(--verify-installed-cli)', public)
        self.assertIn('.installed_cli_verified == true', public)
        workflow = (ROOT / '.github/workflows/native-package-cli-acceptance.yml').read_text()
        for required in ['group: packages-pages', 'cancel-in-progress: false', 'contents: read',
                         '--verify-installed-cli', '--expected-version "$target"',
                         'git show "$artifact_control:$path" | cmp - "$path"',
                         '--expected-control-sha "$artifact_control"', 'if: always()']:
            self.assertIn(required, workflow)
        for forbidden in ['contents: write', 'pages: write', 'id-token: write', 'secrets.', 'environment:',
                          'git push', 'gh release', 'create-github-app-token', 'continue-on-error:']:
            self.assertNotIn(forbidden, workflow)
        self.assertEqual(2, workflow.count('test "$(gh api repos/WuKongIM/packages/git/ref/heads/main --jq .object.sha)" = "$GITHUB_SHA"'))
