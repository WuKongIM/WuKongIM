package routing

import (
	"fmt"
	"testing"
)

func TestSlotMappingMatchesFullRouteFailures(t *testing.T) {
	for _, table := range []*Table{
		nil, {},
		{HashSlotCount: 256, HashToSlot: []uint32{1}, SlotLeaders: map[uint32]uint64{1: 7}},
		{HashSlotCount: 2, HashToSlot: []uint32{0, 1}, SlotLeaders: map[uint32]uint64{}},
		{HashSlotCount: 2, HashToSlot: []uint32{1, 2}, SlotLeaders: map[uint32]uint64{1: 7, 2: 8}},
	} {
		r := NewRouter()
		r.current.Store(table)
		for i := 0; i < 256; i++ {
			key := fmt.Sprintf("channel-%d", i)
			full, fullErr := r.RouteKey(key)
			slot, err := r.SlotForKey(key)
			if slot != full.SlotID || fmt.Sprint(err) != fmt.Sprint(fullErr) {
				t.Fatalf("key=%s slot=%d err=%v; full=%+v err=%v", key, slot, err, full, fullErr)
			}
		}
	}
}
