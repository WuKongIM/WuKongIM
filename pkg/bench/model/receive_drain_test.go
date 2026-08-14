package model

import "testing"

func TestReceiveDrainSnapshotRequiresClosedHealthyZeroCut(t *testing.T) {
	clean := ReceiveDrainSnapshot{
		Required:               true,
		EvidenceComplete:       true,
		DrainComplete:          true,
		ClientCount:            2,
		ActiveDrains:           2,
		QueueSnapshotClients:   2,
		StableZeroObservations: ReceiveDrainStableZeroObservations,
	}
	if !clean.TerminalProofComplete() {
		t.Fatalf("TerminalProofComplete() = false, snapshot = %+v", clean)
	}

	tests := []struct {
		name   string
		mutate func(*ReceiveDrainSnapshot)
	}{
		{name: "missing evidence", mutate: func(s *ReceiveDrainSnapshot) { s.EvidenceComplete = false }},
		{name: "not complete", mutate: func(s *ReceiveDrainSnapshot) { s.DrainComplete = false }},
		{name: "missing client queue", mutate: func(s *ReceiveDrainSnapshot) { s.QueueSnapshotClients-- }},
		{name: "inactive drain", mutate: func(s *ReceiveDrainSnapshot) { s.ActiveDrains-- }},
		{name: "inner recv", mutate: func(s *ReceiveDrainSnapshot) { s.InnerRecvDepth = 1 }},
		{name: "inner receive handoff", mutate: func(s *ReceiveDrainSnapshot) { s.InnerRecvHandoffs = 1 }},
		{name: "adapter", mutate: func(s *ReceiveDrainSnapshot) { s.AdapterQueueDepth = 1 }},
		{name: "adapter handoff", mutate: func(s *ReceiveDrainSnapshot) { s.AdapterHandoffs = 1 }},
		{name: "matching buffer", mutate: func(s *ReceiveDrainSnapshot) { s.MatchingBufferDepth = 1 }},
		{name: "foreground matcher", mutate: func(s *ReceiveDrainSnapshot) { s.ForegroundMatchers = 1 }},
		{name: "read processing", mutate: func(s *ReceiveDrainSnapshot) { s.ReadFramesInFlight = 1 }},
		{name: "recvack", mutate: func(s *ReceiveDrainSnapshot) { s.RecvACKsInFlight = 1 }},
		{name: "publication", mutate: func(s *ReceiveDrainSnapshot) { s.PublicationsInFlight = 1 }},
		{name: "blocked publication", mutate: func(s *ReceiveDrainSnapshot) { s.PublicationWaiters = 1 }},
		{name: "recvack failure", mutate: func(s *ReceiveDrainSnapshot) { s.RecvACKFailures = 1 }},
		{name: "read failure", mutate: func(s *ReceiveDrainSnapshot) { s.ReadFailures = 1 }},
		{name: "unstable zero", mutate: func(s *ReceiveDrainSnapshot) { s.StableZeroObservations-- }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clean
			tt.mutate(&got)
			if got.TerminalProofComplete() {
				t.Fatalf("TerminalProofComplete() = true, snapshot = %+v", got)
			}
		})
	}
}

func TestReceiveDrainSnapshotAllowsExplicitlyUnrequiredDrain(t *testing.T) {
	snapshot := ReceiveDrainNotRequired()
	if !snapshot.TerminalProofComplete() {
		t.Fatalf("TerminalProofComplete() = false, snapshot = %+v", snapshot)
	}

	for name, invalid := range map[string]ReceiveDrainSnapshot{
		"zero value":          {},
		"observed recv frame": func() ReceiveDrainSnapshot { got := snapshot; got.ReceiveFramesObserved = 1; return got }(),
	} {
		t.Run(name, func(t *testing.T) {
			if invalid.TerminalProofComplete() {
				t.Fatalf("TerminalProofComplete() = true, snapshot = %+v", invalid)
			}
		})
	}
}

func TestReceiveDrainFingerprintChangesWithLateReceiveProgress(t *testing.T) {
	before := ReceiveDrainSnapshot{
		Required: true, EvidenceComplete: true, DrainComplete: true,
		ClientCount: 2500, ActiveDrains: 2500, QueueSnapshotClients: 2500,
		StableZeroObservations: ReceiveDrainStableZeroObservations,
	}
	after := before
	after.ReceiveFramesObserved++
	if ReceiveDrainFingerprint(before) == ReceiveDrainFingerprint(after) {
		t.Fatal("late receive progress must change the terminal-cut fingerprint")
	}
	after = before
	after.RecvACKSuccesses++
	if ReceiveDrainFingerprint(before) == ReceiveDrainFingerprint(after) {
		t.Fatal("successful RECVACK progress must change the terminal-cut fingerprint")
	}
	after = before
	after.FanoutProof = FanoutProofSnapshot{
		Version: FanoutProofVersion, Required: true, EvidenceComplete: true,
		Expected: FanoutMultisetSummary{DigestA: string(make([]byte, 64)), DigestB: string(make([]byte, 64))},
	}
	if ReceiveDrainFingerprint(before) == ReceiveDrainFingerprint(after) {
		t.Fatal("fanout multiset progress must change the terminal-cut fingerprint")
	}
	if len(ReceiveDrainFingerprint(before)) != 64 {
		t.Fatalf("fingerprint length = %d, want 64", len(ReceiveDrainFingerprint(before)))
	}
}
