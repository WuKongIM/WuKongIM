package scripts_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleReleasedPreclockRehearsalWiringIsExplicitAndSingleAttempt(t *testing.T) {
	w := string(readWorkflow(t, "chat-lifecycle-rehearsal.yml"))
	s := readFile(t, filepath.Join(repoRoot(t), "scripts/chat-lifecycle/stage-orchestrate.sh"))
	for _, part := range []string{"released_preclock_run_id:", "WK_CHAT_RELEASED_PRECLOCK_RUN_ID: ${{ inputs.released_preclock_run_id }}", "WK_CHAT_ARTIFACT_INVENTORY:", `"$WK_CHAT_ARTIFACT_INVENTORY"`, "chat-lifecycle-rehearsal-continuation-admission-"} {
		if !strings.Contains(w, part) {
			t.Fatalf("missing workflow boundary %q", part)
		}
	}
	a := strings.Index(s, "authenticate-released-preclock.py prepare")
	if a < 0 || a > strings.Index(s, "cloud-deployment-bundle.yml") || a > strings.Index(s, "ssh-keygen -q") {
		t.Fatal("continuation authority must precede all build/key/cloud actions")
	}
	for _, part := range []string{`for attempt in "$stage_attempt"; do`, `--released-preclock-proof "$continuation_proof"`, `--committed-micros "$committed_micros"`, `WK_CHAT_ISSUE_DEDUPE_KEY="${chat_stage}_deployment_repair_${GITHUB_RUN_ID}_${stage_attempt}_${deployment_generation}"`} {
		if !strings.Contains(s, part) {
			t.Fatalf("missing bound materialization %q", part)
		}
	}
	if strings.Count(s, "-f quote_only=false") != 1 || strings.Contains(s, "for attempt in 1 2") {
		t.Fatal("explicit continuation cannot introduce procurement retry")
	}
}
