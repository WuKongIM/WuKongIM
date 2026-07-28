package scripts_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWukongIMThreeNodeMessageEventScriptSetsSafeDefaults(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "bench-wukongim-three-nodes-message-event.sh"))

	for _, want := range []string{
		`PROFILE="${WK_BENCH_MESSAGE_EVENT_PROFILE:-smoke}"`,
		`CLUSTER_INITIAL_SLOT_COUNT="${WK_CLUSTER_INITIAL_SLOT_COUNT:-10}"`,
		`CLUSTER_HASH_SLOT_COUNT="${WK_CLUSTER_HASH_SLOT_COUNT:-256}"`,
		`apply_profile_defaults`,
		`smoke)`,
		`profile_channels=32`,
		`medium)`,
		`profile_channels=1000`,
		`pressure)`,
		`profile_channels=10000`,
		`LIVE_METRICS_INTERVAL="${WK_BENCH_MESSAGE_EVENT_LIVE_METRICS_INTERVAL:-1}"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("message-event script missing default %q", want)
		}
	}
}
