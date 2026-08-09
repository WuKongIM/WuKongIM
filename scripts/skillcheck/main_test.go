package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunAcceptsMinimalSkill(t *testing.T) {
	root := newTestRepo(t)
	writeTestFile(t, root, ".agents/skills/example/SKILL.md", `---
name: example
description: Exercise one deterministic repository task.
---

# Example
`)
	writeTestFile(t, root, ".agents/skills/example/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
  default_prompt: "Use $example for this repository task."
`)

	result := runSkillcheck(root)
	if result.code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr = %q", result.code, result.stderr)
	}
	if result.stdout != "skillcheck: 1 skill valid\n" {
		t.Fatalf("stdout = %q", result.stdout)
	}
	if result.stderr != "" {
		t.Fatalf("stderr = %q, want empty", result.stderr)
	}
}

func TestRunAcceptsRepositoryContracts(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	result := runSkillcheck(root)
	if result.code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr = %q", result.code, result.stderr)
	}
	if !strings.HasSuffix(result.stdout, " skills valid\n") {
		t.Fatalf("stdout = %q, want a plural valid-skill summary", result.stdout)
	}
}

func TestRunRejectsSkillNameThatDoesNotMatchDirectory(t *testing.T) {
	root := newTestRepo(t)
	writeTestFile(t, root, ".agents/skills/example/SKILL.md", `---
name: another-skill
description: Exercise one deterministic repository task.
---
`)
	writeTestFile(t, root, ".agents/skills/example/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
  default_prompt: "Use $example for this repository task."
`)

	want := ".agents/skills/example/SKILL.md: frontmatter name \"another-skill\" must match directory \"example\"\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsMissingSkillDescription(t *testing.T) {
	root := newTestRepo(t)
	writeTestFile(t, root, ".agents/skills/example/SKILL.md", `---
name: example
description: ""
---
`)
	writeTestFile(t, root, ".agents/skills/example/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
  default_prompt: "Use $example for this repository task."
`)

	want := ".agents/skills/example/SKILL.md: frontmatter description must be non-empty\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsIncompleteOpenAIInterface(t *testing.T) {
	root := newTestRepo(t)
	writeTestFile(t, root, ".agents/skills/example/SKILL.md", `---
name: example
description: Exercise one deterministic repository task.
---
`)
	writeTestFile(t, root, ".agents/skills/example/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
`)

	want := ".agents/skills/example/agents/openai.yaml: interface.default_prompt must be non-empty\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsMissingMarkdownReference(t *testing.T) {
	root := newTestRepo(t)
	writeTestFile(t, root, ".agents/skills/example/SKILL.md", `---
name: example
description: Exercise one deterministic repository task.
---

Read [the contract](references/missing.md).
`)
	writeTestFile(t, root, ".agents/skills/example/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
  default_prompt: "Use $example for this repository task."
`)

	want := ".agents/skills/example/SKILL.md: local reference \"references/missing.md\" does not exist\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsMarkdownReferenceThroughEscapingSymlink(t *testing.T) {
	root := newTestRepo(t)
	writeTestFile(t, root, ".agents/skills/example/SKILL.md", `---
name: example
description: Exercise one deterministic repository task.
---

Read [the contract](references/outside.md).
`)
	writeTestFile(t, root, ".agents/skills/example/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
  default_prompt: "Use $example for this repository task."
`)
	writeTestFile(t, root, "outside.md", "outside the skill\n")
	link := filepath.Join(root, ".agents", "skills", "example", "references", "outside.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside.md"), link); err != nil {
		t.Fatal(err)
	}
	writeExampleFocusedTest(t, root)

	want := ".agents/skills/example/SKILL.md: local reference \"references/outside.md\" escapes the skill directory through a symlink\n"
	requireSkillcheckError(t, root, want)
}

func TestRunParsesReferenceLinksAndIgnoresCodeFences(t *testing.T) {
	root := newTestRepo(t)
	writeTestFile(t, root, ".agents/skills/example/SKILL.md", `---
name: example
description: Exercise one deterministic repository task.
---

Read [the contract][rules].

~~~text
[example only](../../../ignored.md)
~~~

[rules]: ../../../outside.md
`)
	writeTestFile(t, root, ".agents/skills/example/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
  default_prompt: "Use this repository skill."
`)
	writeTestFile(t, root, "outside.md", "outside the skill\n")
	writeTestFile(t, root, ".agents/skill-tests.json", `{
  "schema_version": 1,
  "tests": [
    {
      "id": "example-contracts",
      "skill": "example",
      "arguments": ["go", "test", "./scripts/...", "-run", "^TestExample", "-count=1"],
      "timeout_seconds": 20
    }
  ]
}
`)

	want := ".agents/skills/example/SKILL.md: local reference \"../../../outside.md\" escapes the skill directory\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsInvalidJSONFixture(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skills/example/fixtures/broken.json", `{"schema":`)
	writeExampleFocusedTest(t, root)

	want := ".agents/skills/example/fixtures/broken.json: invalid JSON: unexpected EOF\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsInvalidYAMLFixture(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skills/example/fixtures/broken.yaml", "schema: [\n")
	writeExampleFocusedTest(t, root)

	result := runSkillcheck(root)
	if result.code != 1 {
		t.Fatalf("run() code = %d, want 1", result.code)
	}
	wantPrefix := ".agents/skills/example/fixtures/broken.yaml: invalid YAML:"
	if !strings.HasPrefix(result.stderr, wantPrefix) {
		t.Fatalf("stderr = %q, want prefix %q", result.stderr, wantPrefix)
	}
}

func TestRunRejectsNonExecutableSkillScript(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skills/example/scripts/check.sh", "#!/bin/sh\nexit 0\n")
	writeExampleFocusedTest(t, root)

	want := ".agents/skills/example/scripts/check.sh: files under scripts/ must be executable\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsExecutableSkillDataFile(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	path := filepath.Join(root, ".agents", "skills", "example", "SKILL.md")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}

	want := ".agents/skills/example/SKILL.md: files without a shebang must not be executable\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsExecutableFixtureEvenWithShebang(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	fixture := ".agents/skills/example/fixtures/data.yaml"
	writeTestFile(t, root, fixture, "#!/usr/bin/env yaml\nkey: value\n")
	if err := os.Chmod(filepath.Join(root, filepath.FromSlash(fixture)), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExampleFocusedTest(t, root)

	want := ".agents/skills/example/fixtures/data.yaml: files outside scripts/ must not be executable\n"
	requireSkillcheckError(t, root, want)
}

func TestRunAllowsNonExecutableDocumentationUnderScripts(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skills/example/scripts/README.md", "# Helper scripts\n")
	writeExampleFocusedTest(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"--root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
}

func TestRunRejectsDuplicateSkillNames(t *testing.T) {
	root := newTestRepo(t)
	for _, directory := range []string{"alpha", "beta"} {
		writeTestFile(t, root, ".agents/skills/"+directory+"/SKILL.md", `---
name: shared
description: Exercise one deterministic repository task.
---
`)
		writeTestFile(t, root, ".agents/skills/"+directory+"/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
  default_prompt: "Use this repository skill."
`)
	}

	result := runSkillcheck(root)
	if result.code != 1 {
		t.Fatalf("run() code = %d, want 1", result.code)
	}
	want := ".agents/skills/beta/SKILL.md: frontmatter name \"shared\" duplicates .agents/skills/alpha/SKILL.md"
	if !strings.Contains(result.stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", result.stderr, want)
	}
}

func TestRunRejectsInvalidSkillName(t *testing.T) {
	root := newTestRepo(t)
	writeTestFile(t, root, ".agents/skills/Example/SKILL.md", `---
name: Example
description: Exercise one deterministic repository task.
---
`)
	writeTestFile(t, root, ".agents/skills/Example/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
  default_prompt: "Use this repository skill."
`)

	want := ".agents/skills/Example/SKILL.md: frontmatter name must be 1-64 lowercase letters, digits, or single hyphen-separated words\n"
	requireSkillcheckError(t, root, want)
}

func TestRunReportsMissingOpenAIInterfaceOnce(t *testing.T) {
	root := newTestRepo(t)
	writeTestFile(t, root, ".agents/skills/example/SKILL.md", `---
name: example
description: Exercise one deterministic repository task.
---
`)

	want := ".agents/skills/example/agents/openai.yaml: required file is missing\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsFocusedTestOutsideCommandAllowlist(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skill-tests.json", `{
  "schema_version": 1,
  "tests": [
    {
      "id": "unsafe",
      "skill": "example",
      "arguments": ["sh", "-c", "./scripts/discovered.sh"],
      "timeout_seconds": 30
    }
  ]
}
`)

	want := ".agents/skill-tests.json: test \"unsafe\" must be an explicit go test ./scripts/... -run '^Test...' -count=1 command\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsComplexSkillWithoutFocusedTest(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skills/example/references/contract.md", "# Contract\n")

	want := ".agents/skill-tests.json: skill \"example\" has references/, fixtures/, or scripts/ and must register a focused test\n"
	requireSkillcheckError(t, root, want)
}

func TestRunFocusedExecutesOnlyRegisteredTest(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skill-tests.json", `{
  "schema_version": 1,
  "tests": [
    {
      "id": "example-contracts",
      "skill": "example",
      "arguments": ["go", "test", "./scripts/...", "-run", "^TestExample", "-count=1"],
      "timeout_seconds": 30
    }
  ]
}
`)

	var executed [][]string
	executor := func(
		_ context.Context,
		gotRoot string,
		arguments []string,
		_ io.Writer,
		_ io.Writer,
	) error {
		if gotRoot != root {
			t.Fatalf("executor root = %q, want %q", gotRoot, root)
		}
		executed = append(executed, append([]string(nil), arguments...))
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := runWithExecutor(
		[]string{"--root", root, "--run-focused"},
		&stdout,
		&stderr,
		executor,
	); code != 0 {
		t.Fatalf("runWithExecutor() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if len(executed) != 1 {
		t.Fatalf("executed %d commands, want 1", len(executed))
	}
	wantArguments := []string{"go", "test", "./scripts/...", "-run", "^TestExample", "-count=1"}
	if got := strings.Join(executed[0], "\x00"); got != strings.Join(wantArguments, "\x00") {
		t.Fatalf("arguments = %q, want %q", executed[0], wantArguments)
	}
	wantOutput := "skillcheck: running focused test example-contracts (example)\n" +
		"skillcheck: 1 skill valid; 1 focused test passed\n"
	if got := stdout.String(); got != wantOutput {
		t.Fatalf("stdout = %q, want %q", got, wantOutput)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunFocusedBatchesRegisteredTestsUnderSharedBudget(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skill-tests.json", `{
  "schema_version": 1,
  "tests": [
    {
      "id": "example-two-contracts",
      "skill": "example",
      "arguments": ["go", "test", "./scripts/...", "-run", "^TestExampleTwo$", "-count=1"],
      "timeout_seconds": 17
    },
    {
      "id": "example-one-contracts",
      "skill": "example",
      "arguments": ["go", "test", "./scripts/...", "-run", "^TestExampleOne$", "-count=1"],
      "timeout_seconds": 13
    }
  ]
}
`)

	var executed [][]string
	executor := func(
		ctx context.Context,
		_ string,
		arguments []string,
		_ io.Writer,
		_ io.Writer,
	) error {
		executed = append(executed, append([]string(nil), arguments...))
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("focused batch context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining < 29*time.Second || remaining > 30*time.Second {
			t.Fatalf("focused batch deadline remaining = %v, want about 30s", remaining)
		}
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := runWithExecutor(
		[]string{"--root", root, "--run-focused"},
		&stdout,
		&stderr,
		executor,
	); code != 0 {
		t.Fatalf("runWithExecutor() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if len(executed) != 1 {
		t.Fatalf("executed %d commands, want one focused batch", len(executed))
	}
	wantArguments := []string{
		"go", "test", "./scripts/...", "-run",
		"(^TestExampleOne$)|(^TestExampleTwo$)", "-count=1",
	}
	if got := strings.Join(executed[0], "\x00"); got != strings.Join(wantArguments, "\x00") {
		t.Fatalf("arguments = %q, want %q", executed[0], wantArguments)
	}
}

func TestRunRejectsReviewPolicyWithoutFocusedSkillCheck(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".github/review-agent/policy.json", `{
  "trusted_checks": {
    "agent-artifact-contracts": {
      "arguments": ["go", "run", "./scripts/skillcheck"],
      "working_dir": ".",
      "timeout_seconds": 60,
      "max_output_bytes": 1048576
    }
  },
  "path_rules": [
    {
      "name": "agent-skills",
      "paths": [".agents/skill-tests.json"],
      "prefixes": [".agents/skills/"],
      "checks": ["agent-artifact-contracts", "skill-focused-contracts"]
    }
  ]
}
`)

	want := ".github/review-agent/policy.json: trusted check \"skill-focused-contracts\" is missing\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsReviewPolicyWithoutSkillPathRouting(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".github/review-agent/policy.json", `{
  "trusted_checks": {
    "agent-artifact-contracts": {
      "arguments": ["go", "run", "./scripts/skillcheck"],
      "working_dir": ".",
      "timeout_seconds": 60,
      "max_output_bytes": 1048576
    },
    "skill-focused-contracts": {
      "arguments": ["go", "run", "./scripts/skillcheck", "--run-focused"],
      "working_dir": ".",
      "timeout_seconds": 60,
      "max_output_bytes": 1048576
    }
  },
  "path_rules": []
}
`)

	want := ".github/review-agent/policy.json: must route .agents/skills/ and .agents/skill-tests.json through paired static and --run-focused checks\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsReviewPolicyBeyondCombinedGateBudget(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".github/review-agent/policy.json", `{
  "trusted_checks": {
    "agent-artifact-contracts": {
      "arguments": ["go", "run", "./scripts/skillcheck"],
      "working_dir": ".",
      "timeout_seconds": 20,
      "max_output_bytes": 1048576
    },
    "skill-focused-contracts": {
      "arguments": ["go", "run", "./scripts/skillcheck", "--run-focused"],
      "working_dir": ".",
      "timeout_seconds": 41,
      "max_output_bytes": 1048576
    }
  },
  "path_rules": [
    {
      "name": "agent-skills",
      "paths": [".agents/skill-tests.json"],
      "prefixes": [".agents/skills/"],
      "checks": ["agent-artifact-contracts", "skill-focused-contracts"]
    }
  ]
}
`)

	want := ".github/review-agent/policy.json: paired skill checks \"agent-artifact-contracts\" and \"skill-focused-contracts\" declare 61 seconds, exceeding the 60-second gate budget\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsFocusedTestsBeyondFastGateBudget(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skill-tests.json", `{
  "schema_version": 1,
  "tests": [
    {
      "id": "example-one",
      "skill": "example",
      "arguments": ["go", "test", "./scripts/...", "-run", "^TestExampleOne", "-count=1"],
      "timeout_seconds": 25
    },
    {
      "id": "example-two",
      "skill": "example",
      "arguments": ["go", "test", "./scripts/...", "-run", "^TestExampleTwo", "-count=1"],
      "timeout_seconds": 20
    }
  ]
}
`)

	want := ".agents/skill-tests.json: total timeout_seconds must not exceed 40\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsFocusedTestWithNoMatchingGoTest(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".agents/skill-tests.json", `{
  "schema_version": 1,
  "tests": [
    {
      "id": "missing-contracts",
      "skill": "example",
      "arguments": ["go", "test", "./scripts/...", "-run", "^TestMissing$", "-count=1"],
      "timeout_seconds": 20
    }
  ]
}
`)

	want := ".agents/skill-tests.json: test \"missing-contracts\" -run pattern \"^TestMissing$\" matches no default scripts test\n"
	requireSkillcheckError(t, root, want)
}

func TestRunRejectsNonDiscoverableFocusedTestFunction(t *testing.T) {
	for _, test := range []struct {
		name     string
		function string
		pattern  string
	}{
		{
			name:     "lowercase suffix",
			function: "func Testexample(t *testing.T) {}",
			pattern:  "^Testexample$",
		},
		{
			name:     "wrong parameter type",
			function: "func TestWrongSignature(value *testing.B) {}",
			pattern:  "^TestWrongSignature$",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := newTestRepo(t)
			writeMinimalSkill(t, root)
			writeTestFile(t, root, "scripts/example_test.go", "package scripts\n\nimport \"testing\"\n\n"+test.function+"\n")
			writeTestFile(t, root, ".agents/skill-tests.json", fmt.Sprintf(`{
  "schema_version": 1,
  "tests": [
    {
      "id": "non-discoverable",
      "skill": "example",
      "arguments": ["go", "test", "./scripts/...", "-run", %q, "-count=1"],
      "timeout_seconds": 20
    }
  ]
}
`, test.pattern))

			want := fmt.Sprintf(
				".agents/skill-tests.json: test \"non-discoverable\" -run pattern %q matches no default scripts test\n",
				test.pattern,
			)
			requireSkillcheckError(t, root, want)
		})
	}
}

func TestRunRejectsReviewPolicyThatReplacesSkillcheckCommand(t *testing.T) {
	root := newTestRepo(t)
	writeMinimalSkill(t, root)
	writeTestFile(t, root, ".github/review-agent/policy.json", `{
  "trusted_checks": {
    "agent-artifact-contracts": {
      "arguments": ["go", "run", "./scripts/othercheck"],
      "working_dir": ".",
      "timeout_seconds": 15,
      "max_output_bytes": 1048576
    },
    "skill-focused-contracts": {
      "arguments": ["go", "run", "./scripts/othercheck", "--run-focused"],
      "working_dir": ".",
      "timeout_seconds": 45,
      "max_output_bytes": 1048576
    }
  },
  "path_rules": [
    {
      "name": "agent-skills",
      "paths": [".agents/skill-tests.json"],
      "prefixes": [".agents/skills/"],
      "checks": ["agent-artifact-contracts", "skill-focused-contracts"]
    }
  ]
}
`)

	want := ".github/review-agent/policy.json: skill checks must invoke go run ./scripts/skillcheck with only an optional --run-focused argument\n"
	requireSkillcheckError(t, root, want)
}

func writeTestFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeMinimalSkill(t *testing.T, root string) {
	t.Helper()
	writeTestFile(t, root, ".agents/skills/example/SKILL.md", `---
name: example
description: Exercise one deterministic repository task.
---
`)
	writeTestFile(t, root, ".agents/skills/example/agents/openai.yaml", `interface:
  display_name: "Example"
  short_description: "Exercise one repository task"
  default_prompt: "Use this repository skill."
`)
}

type skillcheckResult struct {
	code   int
	stdout string
	stderr string
}

func runSkillcheck(root string) skillcheckResult {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"--root", root}, &stdout, &stderr)
	return skillcheckResult{
		code:   code,
		stdout: stdout.String(),
		stderr: stderr.String(),
	}
}

func requireSkillcheckError(t *testing.T, root string, want string) {
	t.Helper()
	result := runSkillcheck(root)
	if result.code != 1 {
		t.Fatalf("run() code = %d, want 1", result.code)
	}
	if result.stderr != want {
		t.Fatalf("stderr = %q, want %q", result.stderr, want)
	}
	if result.stdout != "" {
		t.Fatalf("stdout = %q, want empty", result.stdout)
	}
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "scripts/example_test.go", `package scripts

import "testing"

func TestExample(t *testing.T) {}
func TestExampleOne(t *testing.T) {}
func TestExampleTwo(t *testing.T) {}
`)
	writeTestFile(t, root, ".agents/skill-tests.json", `{
  "schema_version": 1,
  "tests": []
}
`)
	return root
}

func writeExampleFocusedTest(t *testing.T, root string) {
	t.Helper()
	writeTestFile(t, root, ".agents/skill-tests.json", `{
  "schema_version": 1,
  "tests": [
    {
      "id": "example-contracts",
      "skill": "example",
      "arguments": ["go", "test", "./scripts/...", "-run", "^TestExample", "-count=1"],
      "timeout_seconds": 20
    }
  ]
}
`)
}
