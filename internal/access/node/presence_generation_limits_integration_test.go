//go:build integration

package node

import (
	"fmt"
	"strings"
	"testing"
)

// The original 512-selector contract includes the maximum 65535-byte stored
// UID. A full-generation recheck must not add a smaller arbitrary wire limit.
func TestGenerationSelectorWirePreservesStoredIdentityBatch(t *testing.T) {
	req := generationRequest{Op: generationReadUIDs, Claim: wireGeneration(), Owner: wireCut(), UIDs: make([]string, 512)}
	suffix := strings.Repeat("x", 65532)
	for i := range req.UIDs {
		req.UIDs[i] = fmt.Sprintf("%03d", i) + suffix
	}
	wire, err := encodeGenerationRequest(req)
	if err != nil {
		t.Fatalf("valid original selector batch rejected: %v", err)
	}
	got, err := decodeGenerationRequest(wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.UIDs) != 512 {
		t.Fatal("selector count changed")
	}
	for i := range got.UIDs {
		if got.UIDs[i] != req.UIDs[i] {
			t.Fatalf("selector %d changed", i)
		}
	}
}
