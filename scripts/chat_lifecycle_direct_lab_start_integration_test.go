//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire(t *testing.T) {
	root := repoRoot(t)
	directory := t.TempDir()
	requestID := "chat-20260823T020304Z-b1c2d3e4"
	callLog := filepath.Join(directory, "calls")
	// Start does not invoke these tools; keep accidental calls observable without
	// compiling unused deployment helpers in this controlled process fixture.
	unusedTool := filepath.Join(directory, "unused-control-tool")
	writeDirectLabExecutable(t, unusedTool, `#!/usr/bin/env bash
printf 'unexpected-unused-control-tool\n' >>"$WK_TEST_CALL_LOG"
exit 96
`)
	bundleBuilder := filepath.Join(directory, "build-bundle")
	writeDirectLabExecutable(t, bundleBuilder, `#!/usr/bin/env bash
set -euo pipefail
printf 'build\n' >>"$WK_TEST_CALL_LOG"
[[ "$1" == --source-sha && "$3" == --output-dir ]]
mkdir -p "$4"
printf '{"schema":"wukongim.cloud_deployment.bundle_manifest/v1","source_sha":"%s","control_sha":"%s","bundle_digest":"sha256:%064d"}\n' "$2" "$2" 1 >"$4/bundle-manifest-output.json"
: >"$4/cloud-deployment-bundle.tar.gz"
`)
	chatTool := filepath.Join(directory, "wkchatlifecycle")
	writeDirectLabExecutable(t, chatTool, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$1" >>"$WK_TEST_CALL_LOG"
case "$1" in
  materialize)
    template=''
    while (( $# > 0 )); do
      case "$1" in
        --template) template="$2"; shift 2 ;;
        *) shift ;;
      esac
    done
    jq -e '.workload_duration_seconds == 4500 and .lease_duration_seconds == 21600 and
      .budget.hard_limit_micros == 300000000 and .budget.operational_stop_micros == 250000000' "$template" >/dev/null
    printf '{"schema":"wukongim.chat_lifecycle.run_plan/v1","lease_plan":{"schema":"wukongim.cloud_lease/v1","lease_id":"lease-direct","request_id":"%s","repository":"WuKongIM/WuKongIM","provider":"alibaba","region":"cn-hangzhou"},"bootstrap_access":{"public_keys":["ssh-ed25519 fake"]}}\n' "$WK_TEST_REQUEST_ID"
    ;;
  selector-from-plan|selector)
    printf '{"schema":"wukongim.cloud_lease.selector/v1","selector":{"lease_id":"lease-direct","request_id":"%s","provider":"alibaba","region":"cn-hangzhou","repository":"WuKongIM/WuKongIM","plan_digest":"%064d"}}\n' "$WK_TEST_REQUEST_ID" 2
    ;;
  *) exit 98 ;;
esac
`)
	cloudTool := filepath.Join(directory, "wkcloudlease")
	writeDirectLabExecutable(t, cloudTool, `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$1" >>"$WK_TEST_CALL_LOG"
case "$1" in
  quote)
    printf '{"schema":"wukongim.cloud_lease.quote/v1","quote":{"request_id":"%s","plan_digest":"%064d","capacity_available":true,"quota_available":true,"selection":{"instance_type":"ecs.test"},"zone":"cn-hangzhou-a"}}\n' "$WK_TEST_REQUEST_ID" 2
    ;;
  acquire)
    printf '{"schema":"wukongim.cloud_lease.receipt/v1","receipt":{"lease_id":"lease-direct","request_id":"%s","provider":"alibaba","region":"cn-hangzhou","repository":"WuKongIM/WuKongIM","plan_digest":"%064d","state":"active","resources":[]}}\n' "$WK_TEST_REQUEST_ID" 2
    ;;
  *) exit 97 ;;
esac
`)
	releaseState := filepath.Join(directory, "cloud-lease-release-state")
	writeDirectLabExecutable(t, releaseState, `#!/usr/bin/env bash
set -euo pipefail
printf 'release-%s\n' "$1" >>"$WK_TEST_CALL_LOG"
`)

	command := exec.Command("bash", filepath.Join(root, "scripts", "chat-lifecycle", "direct-lab.sh"), "start", requestID)
	command.Dir = root
	command.Env = append(os.Environ(),
		"WK_CHAT_LAB_STATE_ROOT="+directory,
		"WK_CHAT_LAB_CLOUD_TOOL="+cloudTool,
		"WK_CHAT_LAB_CLOUD_RELEASE_STATE="+releaseState,
		"WK_CHAT_LAB_CHAT_TOOL="+chatTool,
		"WK_CHAT_LAB_GATE_TOOL="+unusedTool,
		"WK_CHAT_LAB_BUNDLE_TOOL="+unusedTool,
		"WK_CHAT_LAB_BUNDLE_BUILDER="+bundleBuilder,
		"WK_CHAT_LAB_PAID_AUTHORIZATION=create-paid-cloud-lease",
		"WK_CHAT_LAB_ALLOW_DIRTY_FOR_TESTS=true",
		"WK_TEST_CALL_LOG="+callLog,
		"WK_TEST_REQUEST_ID="+requestID,
		"ALIBABA_CLOUD_ACCESS_KEY_ID=test-id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET=test-secret",
		"ALIBABA_CLOUD_SECURITY_TOKEN=test-token",
		"WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION=create-and-delete-paid-cloud-lease",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start failed: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(calls), "build\nmaterialize\nquote\nselector-from-plan\nrelease-enable\nacquire\nrelease-enable-after-provision\nselector\n"; got != want {
		t.Fatalf("operation order = %q, want %q", got, want)
	}
	requestDirectory := filepath.Join(directory, requestID)
	for _, name := range []string{"run-policy.json", "materialize-template.json", "run-plan.json", "quote.json", "receipt.json", "release-selector.json", "diagnostic_ed25519", "deployment_ed25519"} {
		info, statErr := os.Stat(filepath.Join(requestDirectory, name))
		if statErr != nil {
			t.Fatalf("missing %s: %v", name, statErr)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s mode = %o, want private", name, info.Mode().Perm())
		}
	}
	state, err := os.ReadFile(filepath.Join(requestDirectory, "state.json"))
	if err != nil || !strings.Contains(string(state), `"state": "active"`) {
		t.Fatalf("state = %s, %v", state, err)
	}
	policy, err := os.ReadFile(filepath.Join(requestDirectory, "run-policy.json"))
	if err != nil || !strings.Contains(string(policy), `"duration_seconds": 3600`) ||
		!strings.Contains(string(policy), `"max_duration_seconds": 4500`) ||
		!strings.Contains(string(policy), `"budget_cny": 300`) ||
		!strings.Contains(string(policy), `"hard_limit_micros": 300000000`) ||
		!strings.Contains(string(policy), `"operational_stop_micros": 250000000`) {
		t.Fatalf("run policy = %s, %v", policy, err)
	}
}
