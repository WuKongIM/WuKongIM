package controller

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

func TestStage10AGofailMarkersArePreserved(t *testing.T) {
	files := map[string][]string{
		"runtime_node_health.go": {
			"// gofail: var wkReportNodeHealthFault string",
			"// if err := gofailReportNodeHealthFault(wkReportNodeHealthFault, req.NodeID); err != nil { return ReportNodeHealthResult{}, err }",
		},
		"runtime_refresh.go": {
			"// gofail: var wkControllerV2StateEventDrop string",
			"// if gofailDropControllerV2StateEvent(wkControllerV2StateEventDrop, st.Revision) { return }",
		},
	}
	for file, wants := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(data)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing marker %q", file, want)
			}
		}
	}
}

func TestGofailReportNodeHealthFaultMatchesNode(t *testing.T) {
	if err := gofailReportNodeHealthFault("", 4); err != nil {
		t.Fatalf("disabled failpoint returned %v", err)
	}
	if err := gofailReportNodeHealthFault("4:paused health", 4); err == nil || !strings.Contains(err.Error(), "paused health") {
		t.Fatalf("node-specific match error = %v, want paused health", err)
	}
	if err := gofailReportNodeHealthFault("4:paused health", 3); err != nil {
		t.Fatalf("non-matching node returned %v", err)
	}
	if err := gofailReportNodeHealthFault("all:paused health", 3); err == nil || !strings.Contains(err.Error(), "paused health") {
		t.Fatalf("all match error = %v, want paused health", err)
	}
}

func TestGofailDropControllerV2StateEventMatchesRevision(t *testing.T) {
	if gofailDropControllerV2StateEvent("", 10) {
		t.Fatal("disabled failpoint dropped event")
	}
	if !gofailDropControllerV2StateEvent("all", 10) {
		t.Fatal("all failpoint did not drop event")
	}
	if !gofailDropControllerV2StateEvent("revision:10", 10) {
		t.Fatal("revision-specific failpoint did not drop matching event")
	}
	if gofailDropControllerV2StateEvent("revision:10", 11) {
		t.Fatal("revision-specific failpoint dropped non-matching event")
	}
}
