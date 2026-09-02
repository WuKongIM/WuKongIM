package flowdoc

import (
	"reflect"
	"strings"
	"testing"
)

func validFlowDocument() string {
	return `---
scope: package
summary: Test navigation card.
---

# pkg/example Flow

## Responsibility

Owns one bounded contract.

## Boundaries

Does not own external orchestration.

## Main Flows

` + "```text" + `
input -> output
## This heading is inside a fence
` + "```" + `

## Invariants and Failure Semantics

Invalid input fails closed.

## Read First

- [Local source](source.go)
- [Parent guide](../guide.md#section)

## Update Triggers

- The contract changes.
`
}

func TestValidateBodyAcceptsCompleteNavigationAndIgnoresFencedHeadings(t *testing.T) {
	if err := ValidateBody([]byte(validFlowDocument())); err != nil {
		t.Fatalf("ValidateBody(): %v", err)
	}
	crlf := strings.ReplaceAll(validFlowDocument(), "\n", "\r\n")
	if err := ValidateBody([]byte(crlf)); err != nil {
		t.Fatalf("ValidateBody(CRLF): %v", err)
	}
	for _, fence := range []string{"```go", " ~~~ text "} {
		if !markdownFence(fence) {
			t.Fatalf("markdownFence(%q) = false", fence)
		}
	}
	if markdownFence("ordinary content") {
		t.Fatal("markdownFence() accepted prose")
	}
	if !sectionHasContent([]string{"", "### Detail", "```", "actual content"}) || sectionHasContent([]string{"", "### Detail", "```", "~~~"}) {
		t.Fatal("sectionHasContent() mishandled structural-only lines")
	}
}

func TestValidateBodyRejectsMissingReorderedExtraAndEmptySections(t *testing.T) {
	valid := validFlowDocument()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "invalid metadata", content: strings.TrimPrefix(valid, "---\n"), want: "front matter"},
		{name: "missing title", content: strings.Replace(valid, "# pkg/example Flow", "prose", 1), want: "title"},
		{name: "wrong title suffix", content: strings.Replace(valid, "# pkg/example Flow", "# pkg/example FLOW", 1), want: "title"},
		{name: "reordered", content: strings.Replace(valid, "## Responsibility", "## Boundaries", 1), want: "Responsibility"},
		{name: "missing final heading", content: strings.Replace(valid, "## Update Triggers", "### Update Triggers", 1), want: "Update Triggers"},
		{name: "extra heading", content: strings.Replace(valid, "## Update Triggers", "## Unexpected\n\ncontent\n\n## Update Triggers", 1), want: "Update Triggers"},
		{name: "second extra heading", content: valid + "\n## Extra\n\ncontent\n", want: "unexpected second-level heading"},
		{name: "empty responsibility", content: strings.Replace(valid, "Owns one bounded contract.", "\n### Detail\n\n```\n```", 1), want: "must not be empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBody([]byte(test.content))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateBody() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLocalReferencesReturnsOnlyDecodedRepositoryPathsInDocumentOrder(t *testing.T) {
	content := []byte(`
[source](source.go)
![diagram](docs/flow%20diagram.svg?raw=1#preview)
[parent](../FLOW.md#read-first)
[root](/docs/README.md)
[anchor](#local)
[query](?mode=raw)
[external](https://example.com/FLOW.md)
[protocol relative](//example.com/FLOW.md)
[mail](mailto:owner@example.com)
`)
	got, err := LocalReferences(content)
	if err != nil {
		t.Fatalf("LocalReferences(): %v", err)
	}
	want := []string{"source.go", "docs/flow diagram.svg", "../FLOW.md", "/docs/README.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LocalReferences() = %v, want %v", got, want)
	}
	if _, err := LocalReferences([]byte(`[bad](bad%zz.md)`)); err == nil {
		t.Fatal("LocalReferences() accepted invalid URL escaping")
	}
}

func TestReadFirstReferencesIsSectionBoundedAndRequiresTheHeading(t *testing.T) {
	content := validFlowDocument()
	got, err := ReadFirstReferences([]byte(content))
	if err != nil {
		t.Fatalf("ReadFirstReferences(): %v", err)
	}
	want := []string{"source.go", "../guide.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadFirstReferences() = %v, want %v", got, want)
	}
	crlf := strings.ReplaceAll(content, "\n", "\r\n")
	got, err = ReadFirstReferences([]byte(crlf))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadFirstReferences(CRLF) = (%v, %v)", got, err)
	}
	if _, err := ReadFirstReferences([]byte("# no section\n")); err == nil {
		t.Fatal("ReadFirstReferences() accepted a document without Read First")
	}
}
