package cluster

import "testing"

func TestRemoteSlotLeaderWorkerCountIsBounded(t *testing.T) {
	tests := []struct {
		peers int
		want  int
	}{
		{peers: 0, want: 0},
		{peers: 1, want: 1},
		{peers: remoteSlotLeaderMaxConcurrency, want: remoteSlotLeaderMaxConcurrency},
		{peers: remoteSlotLeaderMaxConcurrency + 4, want: remoteSlotLeaderMaxConcurrency},
	}
	for _, test := range tests {
		if got := remoteSlotLeaderWorkerCount(test.peers); got != test.want {
			t.Fatalf("remoteSlotLeaderWorkerCount(%d)=%d, want %d", test.peers, got, test.want)
		}
	}
}
