package sim

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderHumanStatus(t *testing.T) {
	status := newStatus("run-1")
	status.setTarget([]string{"http://127.0.0.1:5001"}, []string{"127.0.0.1:5100"})
	status.setTopology(10, 2, 5)
	status.setState(stateRunning)
	status.addMessagesSent(42)

	var out bytes.Buffer
	if err := renderHuman(&out, status.snapshot()); err != nil {
		t.Fatalf("renderHuman() error = %v", err)
	}
	for _, want := range []string{"wkcli sim", "running", "run-1", "messages=42"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("human output missing %q: %s", want, out.String())
		}
	}
}

func TestRenderJSONStatus(t *testing.T) {
	status := newStatus("run-1")

	var out bytes.Buffer
	if err := renderJSON(&out, status.snapshot()); err != nil {
		t.Fatalf("renderJSON() error = %v", err)
	}
	if !strings.Contains(out.String(), `"run_id":"run-1"`) {
		t.Fatalf("json output = %s", out.String())
	}
}
