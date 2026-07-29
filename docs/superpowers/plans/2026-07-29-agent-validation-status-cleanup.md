# Agent Validation Status Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the permanent unscoped Agent validation failure status while preserving the generation-bound fail-closed merge gate.

**Architecture:** Keep the control Workflow's PR-event invalidation job as a summary-only audit job with job-scoped deduplication. Make the generation-bound `Agent Validation Gate` the only invalidation verdict, and enforce that boundary through the existing static Workflow contract parser.

**Tech Stack:** GitHub Actions YAML, Go static contract tests, Markdown operational documentation.

---

### Task 1: Drive the Workflow change with a failing contract

**Files:**
- Modify: `scripts/github_workflows_test.go:1095-1125`
- Test: `scripts/github_workflows_test.go`

- [ ] **Step 1: Replace the old status-write requirement with the desired security contract**

Change the invalidation assertions in
`validateAgentPRValidationControlWorkflow` to:

```go
if len(invalidate.Permissions) != 0 {
	return fmt.Errorf("Agent validation invalidation permissions = %#v, want none", invalidate.Permissions)
}
var invalidateScript strings.Builder
for _, step := range invalidate.Steps {
	invalidateScript.WriteString(step.Run)
	invalidateScript.WriteByte('\n')
}
for _, forbidden := range []string{
	`repos/${GITHUB_REPOSITORY}/statuses/${HEAD_SHA}`,
	`context=Agent Validation Request / PR #${PR_NUMBER}`,
	`state=failure`,
} {
	if strings.Contains(invalidateScript.String(), forbidden) {
		return fmt.Errorf("Agent validation invalidation must not publish %q", forbidden)
	}
}
for _, required := range []string{
	`## Agent validation invalidated`,
	`A fresh Agent validation plan is required.`,
} {
	if !strings.Contains(invalidateScript.String(), required) {
		return fmt.Errorf("Agent validation invalidation summary is missing %q", required)
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
GOWORK=off go test ./scripts \
  -run '^TestAgentPRValidationControlWorkflowContract$' \
  -count=1
```

Expected: FAIL with
`Agent validation invalidation permissions = map[string]string{"statuses":"write"}, want none`.

### Task 2: Make invalidation summary-only

**Files:**
- Modify: `.github/workflows/agent-pr-validation-control.yml:131-164`
- Test: `scripts/github_workflows_test.go`

- [ ] **Step 1: Remove the unscoped status write and write permission**

Replace the invalidation job's permissions and step with:

```yaml
  invalidate:
    name: Invalidate previous Agent validation
    if: github.event.action == 'edited' || github.event.action == 'opened' || github.event.action == 'reopened' || github.event.action == 'synchronize'
    runs-on: ubuntu-24.04
    timeout-minutes: 1
    concurrency:
      group: agent-pr-validation-${{ github.event.pull_request.number }}
      cancel-in-progress: true
    steps:
      - name: Write invalidation summary
        shell: bash
        run: |
          {
            echo '## Agent validation invalidated'
            echo
            echo "- PR: \`#${{ github.event.pull_request.number }}\`"
            echo "- New head SHA: \`${{ github.event.pull_request.head.sha }}\`"
            echo '- A fresh Agent validation plan is required.'
          } >>"$GITHUB_STEP_SUMMARY"
```

- [ ] **Step 2: Run the focused contract test and verify GREEN**

Run:

```bash
GOWORK=off go test ./scripts \
  -run '^TestAgentPRValidationControlWorkflowContract$' \
  -count=1
```

Expected: PASS.

- [ ] **Step 3: Commit the tested Workflow change**

```bash
git add .github/workflows/agent-pr-validation-control.yml \
  scripts/github_workflows_test.go
git commit -m "fix(ci): remove stale Agent validation status"
```

### Task 3: Correct the operational documentation

**Files:**
- Modify: `.github/workflows/README.md:127-131`
- Modify: `docs/development/CI.md:61-79`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Correct the Workflow catalog**

Replace the invalidation paragraph with:

```markdown
Editing, opening, reopening, or adding another commit triggers both safety
Workflows. The control Workflow records an invalidation audit summary and
deduplicates overlapping invalidation jobs; it does not publish an unscoped
classic commit status. The merge-gate check fails closed until the Agent
reassesses the diff and publishes a fresh request. Each such PR event creates a
new merge-gate run ID, which is the validation generation: evidence from an
older generation cannot consume retries or satisfy the new gate.
```

- [ ] **Step 2: Document the single authoritative invalidation signal**

Add this paragraph to `docs/development/CI.md` after the merge-gate overview:

```markdown
The first failing `Agent Validation Gate` attempt is the only PR-event
invalidation verdict. The control Workflow's invalidation job writes an audit
summary only; it must not publish an unscoped classic commit status that cannot
be consumed by a terminal gate generation.
```

- [ ] **Step 3: Record the stable repository rule**

Add this bullet to `docs/development/PROJECT_KNOWLEDGE.md` under `## Internal`:

```markdown
- Agent PR invalidation is represented only by the generation-bound `Agent Validation Gate`; the control invalidation job may deduplicate events and write an audit summary, but must not publish an unscoped classic commit status.
```

- [ ] **Step 4: Commit the documentation**

```bash
git add .github/workflows/README.md \
  docs/development/CI.md \
  docs/development/PROJECT_KNOWLEDGE.md \
  docs/superpowers/plans/2026-07-29-agent-validation-status-cleanup.md
git commit -m "docs: clarify Agent validation invalidation"
```

### Task 4: Verify the complete contract

**Files:**
- Verify: `.github/workflows/*.yml`
- Verify: `scripts/...`

- [ ] **Step 1: Parse every Workflow**

Run:

```bash
ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f) }' \
  .github/workflows/*.yml
```

Expected: exit 0 with no output.

- [ ] **Step 2: Run the focused Agent Workflow contract suite**

Run:

```bash
GOWORK=off go test ./scripts \
  -run '^(TestAgentPRValidation.*|TestAgentWorkflow.*|TestLegacyAutomaticTestWorkflowsAreAbsent)$' \
  -count=1
```

Expected: PASS.

- [ ] **Step 3: Run the complete default scripts unit tier**

Run:

```bash
GOWORK=off go test ./scripts/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Verify repository state and diff**

Run:

```bash
git diff --check origin/main...HEAD
git status --short --branch
git diff --stat origin/main...HEAD
```

Expected: no whitespace errors, a clean worktree, and changes limited to the
design, plan, Workflow, contract test, and operational documentation.
