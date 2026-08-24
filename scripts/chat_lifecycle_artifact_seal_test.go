package scripts_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleShakeoutSealsStoppedLogsEffectiveConfigAndBinaries(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "run-wukongim-three-node-chat-lifecycle-shakeout.sh"))

	for _, want := range []string{
		`source_rebuildable_from_revision`,
		`binary_identity_only`,
		`"$LIFECYCLE_CONFIG"`,
		`"$LOG_DIR"`,
		`"$WUKONGIM_BIN"`,
		`"$WKBENCH_BIN"`,
		`stop_recorded_processes`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("shakeout artifact seal missing %q", want)
		}
	}
	stop := strings.LastIndex(script, "\n  stop_recorded_processes\n")
	seal := strings.LastIndex(script, "\n  write_artifact_checksums\n")
	if stop < 0 || seal < 0 || stop > seal {
		t.Fatal("shakeout must stop every artifact writer before hashing logs")
	}
	for _, forbidden := range []string{"git diff --binary", "git archive"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("shakeout must not copy an unreviewed worktree payload into evidence: %q", forbidden)
		}
	}
}
