package tasks

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSlotReplicaMoveGofailMarkersStayInSource(t *testing.T) {
	source, err := os.ReadFile("slot_replica_move.go")
	if err != nil {
		t.Fatalf("ReadFile(slot_replica_move.go): %v", err)
	}
	text := string(source)
	required := []string{
		"// gofail: var wkSlotReplicaMovePromoteLearnerDelay string",
		"// if err := sleepSlotReplicaMoveFailpoint(ctx, wkSlotReplicaMovePromoteLearnerDelay); err != nil { return err }",
		"// gofail: var wkSlotReplicaMoveTransferLeaderDelay string",
		"// if err := sleepSlotReplicaMoveFailpoint(ctx, wkSlotReplicaMoveTransferLeaderDelay); err != nil { return err }",
		"// gofail: var wkSlotReplicaMoveRemoveVoterDelay string",
		"// if err := sleepSlotReplicaMoveFailpoint(ctx, wkSlotReplicaMoveRemoveVoterDelay); err != nil { return err }",
	}
	for _, marker := range required {
		if !strings.Contains(text, marker) {
			t.Fatalf("slot_replica_move.go missing gofail marker %q", marker)
		}
	}
}

func TestSleepSlotReplicaMoveFailpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepSlotReplicaMoveFailpoint(ctx, "1s"); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepSlotReplicaMoveFailpoint canceled error = %v, want context.Canceled", err)
	}
	if err := sleepSlotReplicaMoveFailpoint(context.Background(), "not-a-duration"); err != nil {
		t.Fatalf("invalid duration should be ignored, got %v", err)
	}
}
