package management

import (
	"os"
	"strings"
	"testing"
)

func TestScaleInChannelDrainInventoryGofailMarkerIsPreserved(t *testing.T) {
	data, err := os.ReadFile("channel_drain.go")
	if err != nil {
		t.Fatalf("read channel_drain.go: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"// gofail: var wkScaleInChannelDrainInventoryFault string",
		"// if err := gofailScaleInChannelDrainInventoryFault(wkScaleInChannelDrainInventoryFault); err != nil {",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing gofail marker %q", want)
		}
	}
}

func TestScaleInChannelDrainInventoryGofailDisabled(t *testing.T) {
	if err := gofailScaleInChannelDrainInventoryFault(""); err != nil {
		t.Fatalf("disabled failpoint returned error: %v", err)
	}
}
