package scripts_test

import (
	"strings"
	"testing"
)

func TestChatLifecyclePreflightArtifactWorkflowsRemainDiagnosticOnly(t *testing.T) {
	for _, stage := range []string{"rehearsal", "formal"} {
		t.Run(stage, func(t *testing.T) {
			workflow := string(readWorkflow(t, "chat-lifecycle-"+stage+".yml"))
			marker := "      - name: Upload read-only Issue preflight receipt\n"
			start := strings.Index(workflow, marker)
			if start < 0 || strings.Count(workflow, marker) != 1 {
				t.Fatal("missing unique diagnostic preflight upload")
			}
			step := workflow[start+len(marker):]
			if end := strings.Index(step, "      - name:"); end >= 0 {
				step = step[:end]
			}
			requestID := "${{ inputs.request_id }}"
			if stage == "formal" {
				requestID = "${{ matrix.request_id }}"
			}
			for _, required := range []string{
				"        if: always()\n",
				"        continue-on-error: true\n",
				"        uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
				"          name: chat-lifecycle-" + stage + "-preflight-" + requestID + "\n",
				"          path: ${{ env.WK_CHAT_OUTPUT_DIR }}/issue-preflight.json\n",
				"          if-no-files-found: ignore\n",
				"          retention-days: 8\n",
			} {
				if !strings.Contains(step, required) {
					t.Fatalf("preflight upload contract missing %q", required)
				}
			}
			if strings.Contains(step, "id:") || strings.Contains(step, "handoff") || strings.Contains(step, "cleanup") || strings.Contains(step, "run:") || strings.Count(step, "path:") != 1 {
				t.Fatalf("preflight upload acquired control flow or additional artifact scope: %s", step)
			}
			if strings.Count(workflow, "issue-preflight.json") != 1 {
				t.Fatal("preflight receipt must not become another handoff/control marker")
			}
		})
	}
}
