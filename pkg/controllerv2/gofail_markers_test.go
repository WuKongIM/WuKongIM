package controllerv2

import (
	"os"
	"strings"
	"testing"
)

func TestMarkNodeRemovedPostCommitGofailMarkerIsPreserved(t *testing.T) {
	data, err := os.ReadFile("runtime_node_lifecycle.go")
	if err != nil {
		t.Fatalf("read runtime_node_lifecycle.go: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"// gofail: var wkMarkNodeRemovedPostCommitFault string",
		"// if err := gofailMarkNodeRemovedPostCommitFault(wkMarkNodeRemovedPostCommitFault); err != nil { return MarkNodeRemovedResult{}, err }",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing gofail marker %q", want)
		}
	}
}

func TestMarkNodeRemovedPostCommitGofailDisabled(t *testing.T) {
	if err := gofailMarkNodeRemovedPostCommitFault(""); err != nil {
		t.Fatalf("disabled failpoint returned error: %v", err)
	}
}
