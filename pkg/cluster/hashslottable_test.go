package cluster

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func assertMigration(t *testing.T, got HashSlotMigration, want HashSlotMigration) {
	t.Helper()

	if got != want {
		t.Fatalf("migration = %#v, want %#v", got, want)
	}
}

func TestHashSlotTableLookup(t *testing.T) {
	table := NewHashSlotTable(256, 4)

	tests := []struct {
		hashSlot uint16
		want     multiraft.SlotID
	}{
		{hashSlot: 0, want: 1},
		{hashSlot: 63, want: 1},
		{hashSlot: 64, want: 2},
		{hashSlot: 127, want: 2},
		{hashSlot: 128, want: 3},
		{hashSlot: 191, want: 3},
		{hashSlot: 192, want: 4},
		{hashSlot: 255, want: 4},
	}

	for _, tt := range tests {
		if got := table.Lookup(tt.hashSlot); got != tt.want {
			t.Fatalf("Lookup(%d) = %d, want %d", tt.hashSlot, got, tt.want)
		}
	}
}

func TestHashSlotTableReassign(t *testing.T) {
	table := NewHashSlotTable(256, 4)

	table.Reassign(64, 1)

	if got := table.Lookup(64); got != 1 {
		t.Fatalf("Lookup(64) = %d, want 1", got)
	}
	if got := table.Version(); got != 2 {
		t.Fatalf("Version() = %d, want 2", got)
	}
}

func TestHashSlotTableHashSlotsOf(t *testing.T) {
	table := NewHashSlotTable(256, 4)

	got := table.HashSlotsOf(1)
	if len(got) != 64 {
		t.Fatalf("len(HashSlotsOf(1)) = %d, want 64", len(got))
	}
	if got[0] != 0 || got[len(got)-1] != 63 {
		t.Fatalf("HashSlotsOf(1) = [%d ... %d], want [0 ... 63]", got[0], got[len(got)-1])
	}
}

func TestHashSlotForKey(t *testing.T) {
	hs1 := HashSlotForKey("test", 256)
	hs2 := HashSlotForKey("test", 256)
	if hs1 != hs2 {
		t.Fatalf("HashSlotForKey returned non-deterministic values: %d != %d", hs1, hs2)
	}
	if hs1 >= 256 {
		t.Fatalf("HashSlotForKey returned %d, want < 256", hs1)
	}

	table := NewHashSlotTable(256, 4)
	slotID := table.Lookup(hs1)
	if slotID < 1 || slotID > 4 {
		t.Fatalf("Lookup(HashSlotForKey(\"test\")) = %d, want [1,4]", slotID)
	}
}

func TestHashSlotTableEncodeDecode(t *testing.T) {
	table := NewHashSlotTable(256, 4)
	table.Reassign(64, 1)

	data := table.Encode()
	decoded, err := DecodeHashSlotTable(data)
	if err != nil {
		t.Fatalf("DecodeHashSlotTable() error = %v", err)
	}

	if decoded.Version() != table.Version() {
		t.Fatalf("decoded Version() = %d, want %d", decoded.Version(), table.Version())
	}
	for hs := uint16(0); hs < 256; hs++ {
		if decoded.Lookup(hs) != table.Lookup(hs) {
			t.Fatalf("decoded Lookup(%d) = %d, want %d", hs, decoded.Lookup(hs), table.Lookup(hs))
		}
	}
}

func TestHashSlotTableMigrationLifecycle(t *testing.T) {
	table := NewHashSlotTable(8, 2)

	table.StartMigration(3, 1, 2)

	got := table.GetMigration(3)
	if got == nil {
		t.Fatal("GetMigration(3) = nil, want migration")
	}
	assertMigration(t, *got, HashSlotMigration{
		HashSlot: 3,
		Source:   1,
		Target:   2,
		Phase:    PhaseSnapshot,
	})

	active := table.ActiveMigrations()
	if len(active) != 1 {
		t.Fatalf("len(ActiveMigrations()) = %d, want 1", len(active))
	}
	assertMigration(t, active[0], *got)

	table.AdvanceMigration(3, PhaseDelta)

	got = table.GetMigration(3)
	if got == nil {
		t.Fatal("GetMigration(3) after AdvanceMigration = nil, want migration")
	}
	assertMigration(t, *got, HashSlotMigration{
		HashSlot: 3,
		Source:   1,
		Target:   2,
		Phase:    PhaseDelta,
	})

	table.FinalizeMigration(3)

	if got := table.GetMigration(3); got != nil {
		t.Fatalf("GetMigration(3) after FinalizeMigration = %#v, want nil", got)
	}
	if got := table.Lookup(3); got != 2 {
		t.Fatalf("Lookup(3) after FinalizeMigration = %d, want 2", got)
	}
	if active := table.ActiveMigrations(); len(active) != 0 {
		t.Fatalf("len(ActiveMigrations()) after FinalizeMigration = %d, want 0", len(active))
	}

	table.StartMigration(4, 2, 1)
	table.AbortMigration(4)

	if got := table.GetMigration(4); got != nil {
		t.Fatalf("GetMigration(4) after AbortMigration = %#v, want nil", got)
	}
	if got := table.Lookup(4); got != 2 {
		t.Fatalf("Lookup(4) after AbortMigration = %d, want 2", got)
	}
}

func TestHashSlotTableClonePreservesMigrationState(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.StartMigration(4, 2, 1)
	table.AdvanceMigration(4, PhaseDelta)

	cloned := table.Clone()
	if cloned == nil {
		t.Fatal("Clone() = nil, want table clone")
	}

	want := []HashSlotMigration{
		{
			HashSlot: 3,
			Source:   1,
			Target:   2,
			Phase:    PhaseSnapshot,
		},
		{
			HashSlot: 4,
			Source:   2,
			Target:   1,
			Phase:    PhaseDelta,
		},
	}
	got := cloned.ActiveMigrations()
	if len(got) != len(want) {
		t.Fatalf("len(ActiveMigrations()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		assertMigration(t, got[i], want[i])
	}

	cloned.AdvanceMigration(3, PhaseDelta)

	gotOriginal := table.GetMigration(3)
	if gotOriginal == nil {
		t.Fatal("original GetMigration(3) = nil, want migration")
	}
	if gotOriginal.Phase != PhaseSnapshot {
		t.Fatalf("original migration phase = %v, want %v", gotOriginal.Phase, PhaseSnapshot)
	}
}

func TestHashSlotTableEncodeDecodePreservesMigrationState(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	table.StartMigration(3, 1, 2)
	table.StartMigration(4, 2, 1)
	table.AdvanceMigration(4, PhaseDelta)

	decoded, err := DecodeHashSlotTable(table.Encode())
	if err != nil {
		t.Fatalf("DecodeHashSlotTable() error = %v", err)
	}

	want := []HashSlotMigration{
		{
			HashSlot: 3,
			Source:   1,
			Target:   2,
			Phase:    PhaseSnapshot,
		},
		{
			HashSlot: 4,
			Source:   2,
			Target:   1,
			Phase:    PhaseDelta,
		},
	}
	got := decoded.ActiveMigrations()
	if len(got) != len(want) {
		t.Fatalf("len(ActiveMigrations()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		assertMigration(t, got[i], want[i])
	}

	gotMigration := decoded.GetMigration(4)
	if gotMigration == nil {
		t.Fatal("decoded GetMigration(4) = nil, want migration")
	}
	assertMigration(t, *gotMigration, want[1])
}
