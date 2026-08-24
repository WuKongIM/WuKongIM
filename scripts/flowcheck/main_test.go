package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportMarksFlowWithoutMetadataInvalid(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/FLOW.md", "# module Flow\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "report"},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "flowcheck: 1 FLOW file; 0 compliant; 1 invalid; 0 warnings\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "module/FLOW.md: FLOW front matter is missing\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCheckRejectsFlowWithoutMetadata(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/FLOW.md", "# module Flow\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "check"},
		&stdout,
		&stderr,
	)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "flowcheck: 1 FLOW file; 0 compliant; 1 invalid; 0 warnings\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "module/FLOW.md: FLOW front matter is missing\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCheckRejectsIncompleteFlowBody(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/FLOW.md", `---
scope: package
summary: Owns one example module.
---

# module Flow

## Responsibility

Own one example.
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "check"},
		&stdout,
		&stderr,
	)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "flowcheck: 1 FLOW file; 0 compliant; 1 invalid; 0 warnings\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "module/FLOW.md: required heading \"## Boundaries\" is missing or out of order\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCheckRejectsEmptyFlowSection(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/FLOW.md", `---
scope: package
summary: Owns one example module.
---

# module Flow

## Responsibility

Own one example.

## Boundaries

## Main Flows

caller -> module

## Invariants and Failure Semantics

Failure is explicit.

## Read First

- [entry.go](entry.go)

## Update Triggers

- Ownership changes.
`)
	writeFlowcheckFile(t, root, "module/entry.go", "package module\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "check"},
		&stdout,
		&stderr,
	)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if got, want := stderr.String(), "module/FLOW.md: section \"## Boundaries\" must not be empty\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCheckRejectsMissingLocalReference(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/FLOW.md", compliantFlow("missing.go"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "check"},
		&stdout,
		&stderr,
	)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if got, want := stderr.String(), "module/FLOW.md: local reference \"missing.go\" does not exist\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCheckRejectsAbsoluteLocalReference(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/FLOW.md", compliantFlow("/entry.go"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "check"},
		&stdout,
		&stderr,
	)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if got, want := stderr.String(), "module/FLOW.md: local reference \"/entry.go\" must be relative\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCheckRequiresBoundedReadFirstReferences(t *testing.T) {
	root := t.TempDir()
	flow := compliantFlow("entry.go")
	flow = strings.ReplaceAll(flow, "- [entry](entry.go)", "Read entry.go first.")
	writeFlowcheckFile(t, root, "module/FLOW.md", flow)
	writeFlowcheckFile(t, root, "module/entry.go", "package module\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "check"},
		&stdout,
		&stderr,
	)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if got, want := stderr.String(), "module/FLOW.md: Read First must contain 1-5 local references\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCheckRejectsFlowOverHardLineLimit(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/entry.go", "package module\n")
	flow := compliantFlow("entry.go")
	for physicalLines(flow) < 151 {
		flow += "Additional detail.\n"
	}
	writeFlowcheckFile(t, root, "module/FLOW.md", flow)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "check"},
		&stdout,
		&stderr,
	)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if got, want := stderr.String(), "module/FLOW.md: 151 lines exceeds the 150-line limit\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRenderProducesCanonicalIndex(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "a/entry.go", "package a\n")
	writeFlowcheckFile(t, root, "a/FLOW.md", compliantFlow("entry.go"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "render"},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	want := `<!-- Code generated by flowcheck; DO NOT EDIT. -->

# FLOW Index

This file is generated from repository FLOW.md metadata.

Regenerate with ` + "`GOWORK=off go run ./scripts/flowcheck --mode render --write-index`" + `.

| Path | Scope | Summary | Lines | Budget |
| --- | --- | --- | ---: | --- |
| [a/FLOW.md](../../a/FLOW.md) | ` + "`package`" + ` | Owns one example module. | 30 | ok |
`
	if got := stdout.String(); got != want {
		t.Fatalf("stdout =\n%s\nwant =\n%s", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCheckRejectsMissingGeneratedIndex(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/entry.go", "package module\n")
	writeFlowcheckFile(t, root, "module/FLOW.md", compliantFlow("entry.go"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "check"},
		&stdout,
		&stderr,
	)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if got, want := stderr.String(), "docs/development/FLOW_INDEX.md: generated index is missing or stale\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestReportWarnsAboutMissingGeneratedIndexWithoutFailing(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/entry.go", "package module\n")
	writeFlowcheckFile(t, root, "module/FLOW.md", compliantFlow("entry.go"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "report"},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got, want := stderr.String(), "docs/development/FLOW_INDEX.md: generated index is missing or stale\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestRenderWritesCanonicalIndexToFixedPath(t *testing.T) {
	root := t.TempDir()
	writeFlowcheckFile(t, root, "module/entry.go", "package module\n")
	writeFlowcheckFile(t, root, "module/FLOW.md", compliantFlow("entry.go"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		[]string{"--root", root, "--mode", "render", "--write-index"},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "flowcheck: wrote docs/development/FLOW_INDEX.md\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	content, err := os.ReadFile(filepath.Join(root, "docs", "development", "FLOW_INDEX.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("| [module/FLOW.md](../../module/FLOW.md) |")) {
		t.Fatalf("generated index lacks module row:\n%s", content)
	}
}

func writeFlowcheckFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func compliantFlow(readFirst string) string {
	return `---
scope: package
summary: Owns one example module.
---

# module Flow

## Responsibility

Own one example.

## Boundaries

The caller owns inputs.

## Main Flows

caller -> module

## Invariants and Failure Semantics

Failure is explicit.

## Read First

- [entry](` + readFirst + `)

## Update Triggers

- Ownership changes.
`
}

func physicalLines(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n")
}
