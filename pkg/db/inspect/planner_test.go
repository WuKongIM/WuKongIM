package inspect

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestPlanMetaPointPartitionFromUID(t *testing.T) {
	query, err := Parse("select uid, token from meta.user where uid='u1' limit 10")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}

	plan, err := planQuery(Options{HashSlotCount: 16}, query)
	if err != nil {
		t.Fatalf("planQuery() err = %v", err)
	}
	if plan.ScanMode != scanModePointPartition {
		t.Fatalf("ScanMode = %q, want %q", plan.ScanMode, scanModePointPartition)
	}
	if !plan.HashSlotSet {
		t.Fatal("HashSlotSet = false, want true")
	}
	if want := cluster.HashSlotForKey("u1", 16); plan.HashSlot != want {
		t.Fatalf("HashSlot = %d, want %d", plan.HashSlot, want)
	}
}

func TestPlanMetaExplicitHashSlot(t *testing.T) {
	query, err := Parse("select * from meta.user where hash_slot=3 limit 10")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}

	plan, err := planQuery(Options{HashSlotCount: 16}, query)
	if err != nil {
		t.Fatalf("planQuery() err = %v", err)
	}
	if plan.ScanMode != scanModeExplicitPartition {
		t.Fatalf("ScanMode = %q, want %q", plan.ScanMode, scanModeExplicitPartition)
	}
	if !plan.HashSlotSet || plan.HashSlot != 3 {
		t.Fatalf("HashSlotSet/HashSlot = %v/%d, want true/3", plan.HashSlotSet, plan.HashSlot)
	}
}

func TestPlanMetaLocalBoundedRequiresHashSlotCount(t *testing.T) {
	query, err := Parse("select * from meta.user limit 10")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}

	_, err = planQuery(Options{}, query)
	if !errors.Is(err, ErrHashSlotRequired) {
		t.Fatalf("planQuery() err = %v, want ErrHashSlotRequired", err)
	}
}

func TestPlanMessageRequiresChannelKey(t *testing.T) {
	query, err := Parse("select * from message.message limit 10")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}

	_, err = planQuery(Options{}, query)
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("planQuery() err = %v, want ErrInvalidQuery", err)
	}
}

func TestPlanMessageCatalog(t *testing.T) {
	query, err := Parse("select * from message.channels limit 10")
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}

	plan, err := planQuery(Options{}, query)
	if err != nil {
		t.Fatalf("planQuery() err = %v", err)
	}
	if plan.ScanMode != scanModeMessageCatalog {
		t.Fatalf("ScanMode = %q, want %q", plan.ScanMode, scanModeMessageCatalog)
	}
}
