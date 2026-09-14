package app

import "testing"

func TestMessageContentFenceTracksEveryRestoreTransition(t *testing.T) {
	a := &App{}
	previous, active := a.messageContentReadFence()
	if active {
		t.Fatal("fresh app fenced")
	}
	for _, want := range []bool{true, true, false, true, false} {
		a.applyRestoreGatewayMaintenance(want)
		current, active := a.messageContentReadFence()
		if current == previous || active != want || a.restoreMaintenance.Load() != want {
			t.Fatalf("transition %t: fence=%d previous=%d active=%t", want, current, previous, active)
		}
		previous = current
	}
}
