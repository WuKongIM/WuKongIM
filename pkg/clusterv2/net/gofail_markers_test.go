package clusternet

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestTransportGofailMarkersStayInSource(t *testing.T) {
	source, err := os.ReadFile("transport.go")
	if err != nil {
		t.Fatalf("ReadFile(transport.go): %v", err)
	}
	text := string(source)
	required := []string{
		"// gofail: var wkClusterNetCallFault string",
		"// if err := gofailClusterNetServiceFault(wkClusterNetCallFault, serviceID); err != nil { return nil, err }",
		"// gofail: var wkClusterNetNotifyFault string",
		"// if err := gofailClusterNetServiceFault(wkClusterNetNotifyFault, serviceID); err != nil { return err }",
	}
	for _, marker := range required {
		if !strings.Contains(text, marker) {
			t.Fatalf("transport.go missing gofail marker %q", marker)
		}
	}
}

func TestGofailClusterNetServiceFaultMatchesAlias(t *testing.T) {
	err := gofailClusterNetServiceFault("node_lifecycle:seed join unavailable", RPCNodeLifecycle)
	if err == nil || !strings.Contains(err.Error(), "seed join unavailable") {
		t.Fatalf("matched error = %v, want seed join unavailable", err)
	}
	if err := gofailClusterNetServiceFault("node_lifecycle:seed join unavailable", RPCControlWrite); err != nil {
		t.Fatalf("non-matching service error = %v, want nil", err)
	}
	if err := gofailClusterNetServiceFault("all:boom", RPCControlWrite); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("all match error = %v, want boom", err)
	}
	if err := gofailClusterNetServiceFault("malformed", RPCControlWrite); !errors.Is(err, errGofailClusterNetFault) {
		t.Fatalf("malformed error = %v, want errGofailClusterNetFault", err)
	}
}
