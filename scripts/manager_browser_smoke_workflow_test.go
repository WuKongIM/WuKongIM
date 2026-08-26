package scripts_test

import (
	"strings"
	"testing"
)

func TestManagerBrowserSmokeWorkflowRunsRealThreeNodeChromiumGate(t *testing.T) {
	raw := string(readWorkflow(t, "manager-browser-smoke.yml"))

	for _, required := range []string{
		"permissions:\n  contents: read",
		"pull_request:",
		"workflow_dispatch:",
		"cmd/wukongim/**",
		"internal/runtime/online/**",
		"pkg/cluster/**",
		"oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6",
		"bun-version: \"1.3.11\"",
		"actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38",
		"node-version: \"22.12.0\"",
		"actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16",
		"bun install --frozen-lockfile",
		"bunx playwright install --with-deps chromium",
		"bun run build",
		"WK_E2E_MANAGER_BROWSER=1 GOWORK=off go test -tags=e2e ./test/e2e/cluster/manager_browser_smoke -count=1 -timeout 5m -p=1 -v",
		"git diff --exit-code HEAD --",
		"web/playwright-report",
		"web/test-results",
	} {
		if !strings.Contains(raw, required) {
			t.Fatalf("Manager browser smoke workflow missing %q", required)
		}
	}

	for _, forbidden := range []string{
		"secrets.",
		"id-token: write",
		"schedule:",
		"WK_MANAGER_E2E_PASSWORD",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("Manager browser smoke workflow unexpectedly contains %q", forbidden)
		}
	}
}
