"""Static contracts for the bounded, credential-free candidate workflow.

Failure inventory is recorded before implementation in the readiness audit.
These tests never build binaries, start a backend, or invoke GitHub.
"""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / '.github/workflows/v3-candidate-regression.yml'


class CandidateWorkflowContract(unittest.TestCase):
    def setUp(self):
        self.source = WORKFLOW.read_text()
        self.runner = (ROOT / 'scripts/run-v3-candidate-check.py').read_text()

    def test_authority_and_trigger_scope(self):
        text = self.source
        self.assertIn('contents: read', text)
        self.assertIn('branches: [main]', text)
        for path in ('"pkg/**"', '"internal/**"', '"test/e2e/message/self_chat/**"',
                     '"test/e2e/message/cross_node_delivery/**"',
                     '"test/e2e/migration/**"', '"test/e2e/suite/**"',
                     '"scripts/v3_candidate_workflow_contract_test.py"'):
            self.assertIn(path, text)
        for denied in ('pull_request_target:', 'secrets.', 'environment:',
                       'contents: write', 'id-token:', 'continue-on-error:',
                       'gh workflow run', 'gh pr ', 'retry:'):
            self.assertNotIn(denied, text)
        actions = re.findall(r'uses:\s+([^\s]+)', text)
        self.assertEqual(len(actions), 6)
        for action in actions:
            self.assertRegex(action, r'^actions/(checkout|setup-go|upload-artifact)@[0-9a-f]{40}$')
        self.assertEqual(text.count('persist-credentials: false'), 2)
        self.assertEqual(text.count('cache: false'), 2)

    def test_frozen_named_check_enforces_policy_limits(self):
        text = self.source
        self.assertIn('git show "$policy_sha:.github/review-agent/policy.json"', text)
        self.assertIn('policy["trusted_checks"]["go-unit"]', text)
        for field in ('arguments', 'working_dir', 'timeout_seconds', 'max_output_bytes'):
            self.assertIn('check["' + field + '"]', text)
        self.assertNotIn('go test ./cmd/', text)
        self.assertIn('GOFLAGS: "-p=2"', text)
        self.assertIn('output_limit', self.runner)
        self.assertIn('signal.SIGKILL', self.runner)
        self.assertIn('start_new_session=True', self.runner)
        self.assertIn('time.monotonic()', self.runner)
        self.assertIn('raise SystemExit', text)

    def test_cleanup_covers_only_owned_session_and_fails_closed(self):
        text = self.runner
        self.assertIn('session_members', text)
        self.assertIn('cleanup_session', text)
        self.assertIn('if session_id == os.getsid(0)', text)
        self.assertIn('if os.getsid(pid) == session_id', text)
        self.assertIn('os.kill(pid, sig)', text)
        self.assertIn('"remaining_processes"', text)
        self.assertIn('"verified"', text)
        self.assertIn('"cleanup_failed"', text)
        self.assertIn('"command_status"', text)
        self.assertIn('finally:', text)
        self.assertNotIn('pkill', text)
        self.assertNotIn('os.killpg', text)
        compile(text, 'scripts/run-v3-candidate-check.py', 'exec')

    def test_sequential_e2e_uses_one_frozen_binary(self):
        text = self.source
        self.assertIn('needs: default-suite', text)
        self.assertEqual(text.count('timeout-minutes: 35'), 2)
        self.assertEqual(text.count('go build -tags=e2e'), 1)
        self.assertIn('WK_E2E_BINARY: ${{ runner.temp }}/v3-candidate-', text)
        self.assertIn('sha256sum --check', text)
        for name in ('ordinary_history', 'cross_node_delivery', 'self_chat'):
            self.assertIn(name, text)
        for exact in ('-count=1 -timeout=12m -p=1 -parallel=1 -json',
                      '-count=1 -timeout=2m -p=1 -json',
                      '-count=1 -timeout=4m -p=1 -json'):
            self.assertIn(exact, text)
        self.assertNotIn(' -run ', text)
        self.assertNotIn('matrix:', text)
        self.assertNotIn('WK_E2E_REPLICA_LOAD_COMPARISON', text)

    def test_source_failure_and_upload_evidence(self):
        text = self.source
        self.assertEqual(text.count('ref: ${{ github.sha }}'), 2)
        self.assertEqual(text.count('git diff --exit-code HEAD --'), 2)
        self.assertEqual(text.count('if: always()'), 4)
        self.assertEqual(text.count('retention-days: 14'), 2)
        self.assertIn('source_sha', text)
        self.assertIn('policy_sha256', text)
        self.assertIn('binary.sha256', text)
        self.assertIn('command.json', self.runner)
        self.assertIn('result.json', text)
        self.assertIn('output_truncated', self.runner)
        self.assertIn('WK_E2E_SELF_CHAT_REPORT', text)
        self.assertIn('scripts/run-v3-candidate-check.py', text)
        self.assertIn('--max-output-bytes 67108864', text)
        self.assertIn('--stdout-name stdout.jsonl --split-output', text)
        self.assertNotIn('timeout --signal=TERM', text)

    def test_embedded_python_is_valid(self):
        lines = self.source.splitlines()
        blocks = []
        for i, line in enumerate(lines):
            if line.strip() == "python3 - <<'PY'":
                indent = len(line) - len(line.lstrip())
                body = []
                for candidate in lines[i + 1:]:
                    if candidate == ' ' * indent + 'PY':
                        break
                    body.append(candidate[indent:])
                blocks.append('\n'.join(body))
        self.assertGreaterEqual(len(blocks), 1)
        for block in blocks:
            compile(block, str(WORKFLOW), 'exec')

    def test_registered_in_existing_catalog(self):
        text = (ROOT / 'scripts/github_workflows_test.go').read_text()
        self.assertIn('"v3-candidate-regression.yml"', text)
        readme = (ROOT / '.github/workflows/README.md').read_text()
        self.assertIn('`v3-candidate-regression.yml`', readme)
        self.assertIn('`go-unit`', readme)


if __name__ == '__main__':
    unittest.main()
