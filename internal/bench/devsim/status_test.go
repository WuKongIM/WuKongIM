package devsim

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusTransitionsAndCounters(t *testing.T) {
	status := NewStatus("dev-sim-run")

	initial := status.Snapshot()
	require.Equal(t, StateStarting, initial.State)
	require.Equal(t, "dev-sim-run", initial.RunID)
	require.NotZero(t, initial.LastTransitionAt)

	status.SetRunning(20, 5, 2)
	status.AddMessagesSent(3)
	status.AddSendErrors(1)
	status.SetLastError("temporary failure")

	snapshot := status.Snapshot()
	require.Equal(t, StateRunning, snapshot.State)
	require.Equal(t, 20, snapshot.ConnectedUsers)
	require.Equal(t, 5, snapshot.PersonChannels)
	require.Equal(t, 2, snapshot.GroupChannels)
	require.Equal(t, uint64(3), snapshot.MessagesSent)
	require.Equal(t, uint64(1), snapshot.SendErrors)
	require.Equal(t, "temporary failure", snapshot.LastError)

	snapshot.LastError = "mutated copy"
	require.Equal(t, "temporary failure", status.Snapshot().LastError)
}
