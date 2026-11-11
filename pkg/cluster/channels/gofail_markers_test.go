package channels

import (
	"os"
	"strings"
	"testing"
)

// The Issue #1077 E2E holds a replica replacement at VerifyMembership through
// this marker; gofail-enabled builds rewrite it, ordinary builds ignore it.
func TestReplicaReplaceVerifyMembershipGofailMarkerStaysInSource(t *testing.T) {
	source, err := os.ReadFile("migration_replica_replace.go")
	if err != nil {
		t.Fatalf("ReadFile(migration_replica_replace.go): %v", err)
	}
	text := string(source)
	for _, marker := range []string{
		"// gofail: var wkChannelReplicaReplaceVerifyMembershipError string",
		`// return fmt.Errorf("gofail: replica replace verify membership held: %s", wkChannelReplicaReplaceVerifyMembershipError)`,
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("migration_replica_replace.go missing gofail marker %q", marker)
		}
	}
}
