package sim

import (
	"reflect"
	"testing"
)

func TestStatusModelSnapshotTracksRunStateCountersAndError(t *testing.T) {
	status := newStatus("run-1")

	status.setTarget([]string{"http://127.0.0.1:5001"}, []string{"127.0.0.1:5100"})
	status.setTopology(10, 2, 3)
	status.setState(stateRunning)
	status.addMessagesSent(3)
	status.addSendErrors(1, "send failed")
	status.addRecv(7, 2)
	status.addReconnect("connection reset")

	snapshot := status.snapshot()
	if snapshot.State != stateRunning {
		t.Fatalf("State = %q, want %q", snapshot.State, stateRunning)
	}
	if snapshot.RunID != "run-1" {
		t.Fatalf("RunID = %q, want run-1", snapshot.RunID)
	}
	if !reflect.DeepEqual(snapshot.TargetServers, []string{"http://127.0.0.1:5001"}) {
		t.Fatalf("TargetServers = %#v", snapshot.TargetServers)
	}
	if !reflect.DeepEqual(snapshot.GatewayTCPAddrs, []string{"127.0.0.1:5100"}) {
		t.Fatalf("GatewayTCPAddrs = %#v", snapshot.GatewayTCPAddrs)
	}
	if snapshot.Users != 10 || snapshot.ActiveUsers != 10 || snapshot.Groups != 2 || snapshot.GroupMembers != 3 {
		t.Fatalf("topology = users %d active %d groups %d members %d", snapshot.Users, snapshot.ActiveUsers, snapshot.Groups, snapshot.GroupMembers)
	}
	if snapshot.MessagesSent != 3 || snapshot.SendErrors != 1 || snapshot.RecvMessages != 7 || snapshot.RecvDropped != 2 || snapshot.Reconnects != 1 {
		t.Fatalf("counters = %#v", snapshot)
	}
	if snapshot.LastError != "connection reset" {
		t.Fatalf("LastError = %q, want connection reset", snapshot.LastError)
	}
	if snapshot.LastTransitionAt.IsZero() {
		t.Fatalf("LastTransitionAt was not set")
	}
}
