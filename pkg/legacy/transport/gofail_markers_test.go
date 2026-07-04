package transport

import (
	"os"
	"strings"
	"testing"
)

func TestPoolGofailMarkersStayInSource(t *testing.T) {
	source, err := os.ReadFile("pool.go")
	if err != nil {
		t.Fatalf("ReadFile(pool.go): %v", err)
	}
	text := string(source)

	required := []string{
		"// gofail: var wkTransportSendFault string",
		"// return errors.New(wkTransportSendFault)",
		"// gofail: var wkTransportRPCFault string",
		"// return nil, errors.New(wkTransportRPCFault)",
	}
	for _, marker := range required {
		if !strings.Contains(text, marker) {
			t.Fatalf("pool.go missing gofail marker %q", marker)
		}
	}
	for _, oldMarker := range []string{"wkTransportSendDelay", "wkTransportRPCDelay"} {
		if strings.Contains(text, oldMarker) {
			t.Fatalf("pool.go should use one failpoint per transport path, found obsolete marker %q", oldMarker)
		}
	}
}
