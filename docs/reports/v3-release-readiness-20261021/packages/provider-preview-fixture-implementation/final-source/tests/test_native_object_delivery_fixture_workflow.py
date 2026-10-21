"""Static contracts for the separate, credential-free native delivery fixture step."""
import re
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / '.github/workflows/native-package-repository-drill.yml'
RUN = 'Verify native TEST ONLY preview and stable delivery fixtures'
UPLOAD = 'Preserve public native delivery fixture evidence'


class NativeDeliveryFixtureWorkflowTests(unittest.TestCase):
    def step(self, name):
        text = WORKFLOW.read_text()
        match = re.search(r'^      - name: ' + re.escape(name) + r'\n.*?(?=^      - name:|^  [a-z]|\Z)',
                          text, re.MULTILINE | re.DOTALL)
        self.assertIsNotNone(match, 'missing required native fixture workflow step: ' + name)
        return match.group()

    def test_real_driver_is_executed_with_exact_checkout_and_trusted_tools(self):
        step = self.step(RUN)
        for token in ('set -euo pipefail', 'manifests/signing-toolchain.json',
                      'python3 -B /control/tests/native_object_delivery_fixture_drill.py',
                      '--source-sha "$GITHUB_SHA"', '--trusted-root /control/.trusted/wukongim',
                      '--output-dir /evidence'):
            self.assertIn(token, step)
        self.assertNotIn('continue-on-error', step)
        self.assertNotIn('|| true', step.replace("trap 'docker rm --force \"$container\" >/dev/null 2>&1 || true' EXIT", ''))

    def test_zero_omitted_cases_and_cleanup_are_required(self):
        step = self.step(RUN)
        for token in ('.passed == true', '.test_only == true', '.scratch_removed == true',
                      '.secret_tree_removed == true', '(.cases | length) == 29',
                      'all(.cases[]; .passed == true)', '.verification.native_preview_verified == true',
                      '.verification.native_stable_verified == true',
                      '.verification.github_provenance_verified == false',
                      '.verification.fresh_production_auth_verified == false',
                      '.verification.activation_authorized == false'):
            self.assertIn(token, step)
        self.assertIn('result.json', step)

    def test_execution_is_bounded_and_has_no_external_authority(self):
        step = self.step(RUN)
        for token in ('timeout --signal=TERM --kill-after=10s 15m docker run --rm',
                      '--init', '--network none', '--read-only', '--cap-drop ALL', '--security-opt no-new-privileges',
                      '--user 65532:65532', '--cpus 1', '--memory 512m', '--pids-limit 128',
                      '--tmpfs /tmp:rw,nosuid,nodev,size=536870912',
                      '"$GITHUB_WORKSPACE:/control:ro"', 'trap ', 'docker rm --force'):
            self.assertIn(token, step)
        for forbidden in ('secrets.', 'id-token:', 'write', '--privileged', '--network host', 'continue-on-error'):
            self.assertNotIn(forbidden, step)
        self.assertIn('permissions:\n  contents: read\n', WORKFLOW.read_text())

    def test_public_receipts_are_preserved_on_failure(self):
        step = self.step(UPLOAD)
        for token in ('if: always()', 'actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a',
                      'native-object-delivery-fixture-test-only', 'native-delivery-fixture/result.json',
                      'native-delivery-fixture/fixture-export.json', 'if-no-files-found: warn'):
            self.assertIn(token, step)
        self.assertNotIn('native-delivery-fixture/', step.replace('native-delivery-fixture/result.json', '')
                         .replace('native-delivery-fixture/fixture-export.json', ''))

    def test_legacy_native_and_client_suites_remain_separate(self):
        text = WORKFLOW.read_text()
        self.assertIn('"tests/**"', text)
        self.assertIn('"docs/**"', text)
        self.assertIn('python3 -B /control/tests/native_stable_candidate_drill.py', text)
        self.assertIn('(.cases | length) == 31', text)
        self.assertIn('python3 -B tests/native_stable_clients_drill.py', text)
        self.assertIn('(.cases | length) == 49', text)


if __name__ == '__main__':
    unittest.main()
