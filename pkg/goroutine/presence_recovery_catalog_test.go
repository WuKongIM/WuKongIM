package goroutine

import "testing"

func TestRecoverySchedulerHasDistinctBoundedTaskIdentity(t *testing.T) {
	found := false
	for _, entry := range defaultTaskCatalog {
		if entry.ID == TaskID("presence/recovery_scheduler") {
			found = true
			if entry.Module != ModulePresence || entry.Kind != TaskKindDynamic || entry.PanicPolicy != PanicPolicyRepanic {
				t.Fatalf("wrong recovery scheduler ownership policy: %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("recovery scheduler has no dedicated task identity")
	}
}
