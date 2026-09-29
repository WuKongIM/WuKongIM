//go:build e2e

package medium_recipient_hotpath

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// These cases precede the stall observer: a received message is not a SENDACK,
// completed SENDACKs must not look stalled, and retention must stay bounded.
func TestPermissionSoakStallSeparatesAckFromReceive(t *testing.T) {
	tracker := newPermissionSoakTracker()
	now := time.Now()
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("wkrc-permission-soak-%09d", i)
		tracker.begin(key, 1)
		value, _ := tracker.starts.Load(key)
		value.(*permissionSoakMessageStart).startedAt = now.Add(-time.Second)
	}
	if _, err := tracker.observeSendack("wkrc-permission-soak-000000001"); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.observeRecv("wkrc-permission-soak-000000002"); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.observeRecv("wkrc-permission-soak-000000003"); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.observeSendack("wkrc-permission-soak-000000003"); err != nil {
		t.Fatal(err)
	}
	got := tracker.stallSnapshot(now, 25)
	if got.PendingACKs != 1 || len(got.Senders) != 1 || got.Senders[0].Ordinal != 2 || got.Senders[0].AgeMS != 1000 || got.Senders[0].Sender != 1 || got.Senders[0].ChannelIndex != 1 {
		t.Fatalf("RECV and SENDACK were confused: %+v", got)
	}
}

func TestPermissionSoakStallBoundsAndSelectsOldestPerSender(t *testing.T) {
	tracker := newPermissionSoakTracker()
	now := time.Now()
	for i := 1; i <= 2500; i++ {
		key := fmt.Sprintf("wkrc-permission-soak-%09d", i)
		tracker.begin(key, 1)
		value, _ := tracker.starts.Load(key)
		value.(*permissionSoakMessageStart).startedAt = now.Add(-time.Duration(2501-i) * time.Millisecond)
	}
	got := tracker.stallSnapshot(now, 50)
	if got.PendingACKs != 2500 || len(got.Senders) != 25 {
		t.Fatalf("unbounded or lost state: %+v", got)
	}
	for i, sender := range got.Senders {
		if sender.Sender != i || sender.PendingACKs != 100 || sender.Ordinal != i+1 || sender.ChannelIndex != i {
			t.Fatalf("wrong oldest channel mapping: %+v", sender)
		}
	}
}

func TestPermissionSoakStallRejectsMalformedOrdinals(t *testing.T) {
	tracker := newPermissionSoakTracker()
	for _, key := range []string{"private-identity", "wkrc-permission-soak-000000000", "wkrc-permission-soak--00000001", "wkrc-permission-soak-99999999999999999999999"} {
		tracker.begin(key, 1)
	}
	got := tracker.stallSnapshot(time.Now(), 25)
	if got.InvalidKeys != 4 || got.PendingACKs != 0 || len(got.Senders) != 0 {
		t.Fatalf("invalid selector accepted: %+v", got)
	}
}

func TestPermissionSoakStallRingExpiresAndFreezes(t *testing.T) {
	probe := &permissionSoakStallProbe{}
	for i := 0; i < 50; i++ {
		probe.record(permissionSoakStallSample{OffsetMS: float64(i * 250)})
	}
	got := probe.freeze(12500)
	if got.Samples != 50 || len(got.Recent) != 32 || got.Recent[0].OffsetMS != 4500 || got.Recent[31].OffsetMS != 12250 {
		t.Fatalf("wrong bounded ring: %+v", got)
	}
	probe.record(permissionSoakStallSample{OffsetMS: 13000})
	if !reflect.DeepEqual(got, probe.freeze(14000)) {
		t.Fatal("frozen evidence changed")
	}
	idle := &permissionSoakStallProbe{}
	idle.record(permissionSoakStallSample{OffsetMS: 0})
	if got := idle.freeze(9000); got.Samples != 1 || len(got.Recent) != 0 {
		t.Fatal("stale state looks recent")
	}
}
