package skillcontracts_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestChatLifecycleProjectSkillKeepsPaidAuthorityAndOperationsSeparate(t *testing.T) {
	root := repoRoot(t)
	skill := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-chat-lifecycle", "SKILL.md"))
	reference := readFile(t, filepath.Join(root, ".agents", "skills", "wukongim-chat-lifecycle", "references", "operator-workflow.md"))
	for _, required := range []string{
		"name: wukongim-chat-lifecycle",
		"开始聊天生命周期全流程压测",
		"CNY 1,500",
		"status, diagnose, stop, explanations, and next-step questions never do",
		"chat-lifecycle-rehearsal.yml",
		"chat-lifecycle-stop.yml",
		"$wukongim-cloud-analysis",
		"30-minute monitor",
	} {
		if !strings.Contains(skill, required) {
			t.Fatalf("project Skill is missing %q", required)
		}
	}
	for _, required := range []string{
		"wukongim-leases/chat-lifecycle/<request_id>",
		"wkchatlifecycle open-access",
		"operator-stop-chat-lifecycle",
		"deployment_repair_pending",
		"Chat-Lifecycle-Repair: <request_id>",
		"zero-inventory proof",
		"UTC and Asia/Shanghai",
		"local-request-state.sh cleanup",
	} {
		if !strings.Contains(reference, required) {
			t.Fatalf("project Skill operator reference is missing %q", required)
		}
	}
}
