package hashslot

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestHashSlotTableDistributesAndRoutesDeterministically(t *testing.T) {
	table := NewHashSlotTable(10, 3)

	if got, want := table.HashSlotsOf(1), []uint16{0, 1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("slot 1 hash slots = %v, want %v", got, want)
	}
	if got, want := table.HashSlotsOf(2), []uint16{4, 5, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("slot 2 hash slots = %v, want %v", got, want)
	}
	if got, want := table.HashSlotsOf(3), []uint16{7, 8, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("slot 3 hash slots = %v, want %v", got, want)
	}
	if got, want := table.AssignedSlotIDs(), []multiraft.SlotID{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assigned slot IDs = %v, want %v", got, want)
	}
	if got := table.Lookup(10); got != 0 {
		t.Fatalf("out-of-range lookup = %d, want 0", got)
	}
	if got := HashSlotForKey("stable-key", table.HashSlotCount()); got != HashSlotForKey("stable-key", table.HashSlotCount()) || got >= 10 {
		t.Fatalf("stable-key hash slot = %d, want deterministic value in [0,10)", got)
	}
	if got := HashSlotForKey("stable-key", 0); got != 0 {
		t.Fatalf("zero-count hash slot = %d, want 0", got)
	}
}

func TestHashSlotMigrationLifecyclePreservesAuthorityUntilFinalize(t *testing.T) {
	table := NewHashSlotTable(8, 2)
	baseVersion := table.Version()

	// Invalid or duplicate starts must not create ambiguous migration state.
	table.StartMigration(0, 2, 3)
	table.StartMigration(8, 1, 3)
	table.StartMigration(0, 1, 1)
	if got := table.Version(); got != baseVersion {
		t.Fatalf("version after rejected migrations = %d, want %d", got, baseVersion)
	}

	table.StartMigration(0, 1, 3)
	table.StartMigration(0, 1, 4)
	if got := table.Lookup(0); got != 1 {
		t.Fatalf("lookup during migration = %d, want source 1", got)
	}
	if got := table.GetMigration(0); got == nil || got.Source != 1 || got.Target != 3 || got.Phase != PhaseSnapshot {
		t.Fatalf("migration after start = %#v", got)
	}

	table.AdvanceMigration(0, PhaseDelta)
	table.AdvanceMigration(0, PhaseDelta)
	if got := table.GetMigration(0); got == nil || got.Phase != PhaseDelta {
		t.Fatalf("migration after advance = %#v", got)
	}

	clone := table.Clone()
	clone.AbortMigration(0)
	if clone.GetMigration(0) != nil || table.GetMigration(0) == nil {
		t.Fatal("clone mutation changed the original migration map")
	}

	table.FinalizeMigration(0)
	if got := table.Lookup(0); got != 3 {
		t.Fatalf("lookup after finalize = %d, want target 3", got)
	}
	if got := table.GetMigration(0); got != nil {
		t.Fatalf("migration after finalize = %#v, want nil", got)
	}

	table.StartMigration(1, 1, 3)
	table.AbortMigration(1)
	if got := table.Lookup(1); got != 1 {
		t.Fatalf("lookup after abort = %d, want source 1", got)
	}
}

func TestHashSlotTableEncodingRoundTripAndLegacyCompatibility(t *testing.T) {
	table := NewHashSlotTable(6, 2)
	table.Reassign(5, 4)
	table.StartMigration(0, 1, 3)
	table.AdvanceMigration(0, PhaseSwitching)

	decoded, err := DecodeHashSlotTable(table.Encode())
	if err != nil {
		t.Fatalf("DecodeHashSlotTable(v2): %v", err)
	}
	if !reflect.DeepEqual(decoded.assignment, table.assignment) || !reflect.DeepEqual(decoded.ActiveMigrations(), table.ActiveMigrations()) || decoded.Version() != table.Version() {
		t.Fatalf("decoded table = %#v, want %#v", decoded, table)
	}

	legacy := make([]byte, 0, 12+len(table.assignment)*8)
	legacy = binary.BigEndian.AppendUint16(legacy, 1)
	legacy = binary.BigEndian.AppendUint16(legacy, table.HashSlotCount())
	legacy = binary.BigEndian.AppendUint64(legacy, table.Version())
	for _, slotID := range table.assignment {
		legacy = binary.BigEndian.AppendUint64(legacy, uint64(slotID))
	}
	decodedLegacy, err := DecodeHashSlotTable(legacy)
	if err != nil {
		t.Fatalf("DecodeHashSlotTable(v1): %v", err)
	}
	if !reflect.DeepEqual(decodedLegacy.assignment, table.assignment) || len(decodedLegacy.ActiveMigrations()) != 0 {
		t.Fatalf("decoded legacy table = %#v", decodedLegacy)
	}
}

func TestDecodeHashSlotTableRejectsTruncatedAndUnknownData(t *testing.T) {
	valid := NewHashSlotTable(2, 1).Encode()
	tests := map[string][]byte{
		"empty":                 nil,
		"unknown version":       append([]byte{0, 9}, valid[2:]...),
		"truncated assignments": valid[:len(valid)-11],
		"truncated migration count": func() []byte {
			data := append([]byte(nil), valid...)
			return data[:len(data)-1]
		}(),
		"unexpected migration bytes": append(append([]byte(nil), valid...), 1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeHashSlotTable(data); !errors.Is(err, ErrInvalidTable) {
				t.Fatalf("error = %v, want ErrInvalidTable", err)
			}
		})
	}
}

func TestHashSlotPlansAreDeterministicAndReachBalancedOwnership(t *testing.T) {
	t.Run("add slot", func(t *testing.T) {
		table := NewHashSlotTable(10, 2)
		plan := ComputeAddSlotPlan(table, 3)
		if got, want := plan, []MigrationPlan{{HashSlot: 9, From: 2, To: 3}, {HashSlot: 4, From: 1, To: 3}, {HashSlot: 8, From: 2, To: 3}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("add plan = %v, want %v", got, want)
		}
		applyMigrationPlan(table, plan)
		assertBalancedCounts(t, table, []multiraft.SlotID{1, 2, 3})
		if got := ComputeAddSlotPlan(table, 3); got != nil {
			t.Fatalf("adding existing slot plan = %v, want nil", got)
		}
	})

	t.Run("remove slot", func(t *testing.T) {
		table := NewHashSlotTable(10, 3)
		plan := ComputeRemoveSlotPlan(table, 2)
		if len(plan) != 3 {
			t.Fatalf("remove plan length = %d, want 3: %v", len(plan), plan)
		}
		applyMigrationPlan(table, plan)
		if got := table.HashSlotsOf(2); len(got) != 0 {
			t.Fatalf("removed slot still owns %v", got)
		}
		assertBalancedCounts(t, table, []multiraft.SlotID{1, 3})
	})

	t.Run("rebalance", func(t *testing.T) {
		table := NewHashSlotTable(12, 3)
		for hashSlot := uint16(4); hashSlot < 7; hashSlot++ {
			table.Reassign(hashSlot, 1)
		}
		plan := ComputeRebalancePlan(table)
		if len(plan) == 0 || !reflect.DeepEqual(plan, ComputeRebalancePlan(table)) {
			t.Fatalf("rebalance plan = %v, want non-empty deterministic plan", plan)
		}
		applyMigrationPlan(table, plan)
		assertBalancedCounts(t, table, []multiraft.SlotID{1, 2, 3})
	})
}

func TestHashSlotPlanRejectsMissingAuthority(t *testing.T) {
	if got := ComputeAddSlotPlan(nil, 1); got != nil {
		t.Fatalf("add plan for nil table = %v", got)
	}
	if got := ComputeRemoveSlotPlan(NewHashSlotTable(4, 1), 1); got != nil {
		t.Fatalf("remove only slot plan = %v", got)
	}
	if got := ComputeRemoveSlotPlan(NewHashSlotTable(4, 2), 9); got != nil {
		t.Fatalf("remove absent slot plan = %v", got)
	}
	if got := ComputeRebalancePlan(NewHashSlotTable(4, 1)); got != nil {
		t.Fatalf("single-slot rebalance plan = %v", got)
	}
}

func applyMigrationPlan(table *HashSlotTable, plan []MigrationPlan) {
	for _, item := range plan {
		table.Reassign(item.HashSlot, item.To)
	}
}

func assertBalancedCounts(t *testing.T, table *HashSlotTable, slots []multiraft.SlotID) {
	t.Helper()
	min, max := int(table.HashSlotCount()), 0
	for _, slotID := range slots {
		count := len(table.HashSlotsOf(slotID))
		if count < min {
			min = count
		}
		if count > max {
			max = count
		}
	}
	if max-min > 1 {
		t.Fatalf("ownership is not balanced: min=%d max=%d", min, max)
	}
}
