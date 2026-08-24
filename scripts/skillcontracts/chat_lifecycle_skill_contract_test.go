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
		"开始聊天生命周期稳定性长跑",
		"CNY 300",
		"deploy, run, status, diagnose, stop, explanations, approvals, and next-step questions never buy servers",
		"scripts/chat-lifecycle/direct-lab.sh preflight",
		"Codex owns this loop from the local machine",
		"within 15 seconds",
		"default qualification duration is 60 minutes",
		"automatically adds a 15-minute control reserve",
		"`--duration 72h`",
		"zero-inventory proof",
		"Manager URL, Manager username, Manager password, and Demo",
		"final operator-facing response",
		"private `access.json`",
	} {
		if !strings.Contains(skill, required) {
			t.Fatalf("project Skill is missing %q", required)
		}
	}
	for _, required := range []string{
		"wukongim-leases/chat-lifecycle-direct/<request_id>",
		"ALIBABA_CLOUD_SECURITY_TOKEN",
		"WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION",
		"direct-lab.sh start",
		"--duration \"$qualification_duration\"",
		"direct-lab.sh deploy",
		"direct-lab.sh run",
		"direct-lab.sh diagnose",
		"direct-lab.sh status",
		"direct-lab.sh stop",
		"not delete and repurchase servers between generations",
		"Manager: <manager_url>",
		"Manager username: <username>",
		"Manager password: <password>",
		"Demo: <demo_url>",
		"Demo HTTP Basic Auth: same username and password as Manager",
		"typed readiness gate has not passed",
	} {
		if !strings.Contains(reference, required) {
			t.Fatalf("project Skill operator reference is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"gh workflow run", ".github/workflows/chat-lifecycle-repair.yml", "only paid entrypoint",
		"开始聊天生命周期修复短跑",
	} {
		if strings.Contains(skill+reference, forbidden) {
			t.Fatalf("direct project Skill retains GitHub repair orchestration %q", forbidden)
		}
	}
}
