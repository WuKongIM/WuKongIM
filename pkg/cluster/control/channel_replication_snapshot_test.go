package control

import (
	"testing"
	"time"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// Leaders read the committed activation from the node snapshot; it must not
// be inferred from anything but the Controller section.
func TestSnapshotReportsChannelAuthorityPromiseActivation(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	st := controller.ClusterState{Revision: 9}
	if snap := snapshotFromControllerState(st, 1, now, 30*time.Second); snap.ChannelAuthorityPromiseActive {
		t.Fatal("inactive Controller state reported active")
	}
	st.ChannelAuthorityPromise = &controller.ChannelAuthorityPromiseState{ActivatedIndex: 9}
	if snap := snapshotFromControllerState(st, 1, now, 30*time.Second); !snap.ChannelAuthorityPromiseActive {
		t.Fatal("active Controller state reported inactive")
	}
}
