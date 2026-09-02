package raft

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceProposalFutureReportsLifecycleWithoutElectionWait(t *testing.T) {
	dir := t.TempDir()
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          []Peer{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap: true,
		RaftDir:        filepath.Join(dir, "controller-raft"),
		StateMachine:   newTestStateMachine(t, filepath.Join(dir, "cluster-state.json")),
		Transport:      discardRecoveryTransport{},
		TickInterval:   time.Hour,
	})
	require.NoError(t, err)

	require.ErrorIs(t, service.ProbePropose(context.Background()), ErrNotStarted)
	require.NoError(t, service.Start(context.Background()))
	require.NoError(t, service.Stop())
	require.ErrorIs(t, service.ProbePropose(context.Background()), ErrStopped)
}

func TestServiceCompactionReportsNotStartedWithoutOpeningStorage(t *testing.T) {
	dir := t.TempDir()
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          []Peer{{NodeID: 1, Addr: "n1"}},
		AllowBootstrap: true,
		RaftDir:        filepath.Join(dir, "controller-raft"),
		StateMachine:   newTestStateMachine(t, filepath.Join(dir, "cluster-state.json")),
		Transport:      discardRecoveryTransport{},
	})
	require.NoError(t, err)

	result, err := service.CompactLog(context.Background())
	require.ErrorIs(t, err, ErrNotStarted)
	require.False(t, result.Compacted)
	require.Equal(t, LogCompactionSkipNotStarted, result.SkippedReason)
	require.Equal(t, LogCompactionSkipNotStarted, service.Status().Compaction.SkippedReason)
}
