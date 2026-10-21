package replication

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestQuorumLogInstallPreservesRecoveryFailureIdentity(t *testing.T) {
	storageErr := errors.New("local storage read failed")
	for _, test := range []struct {
		name      string
		fault     string
		want      error
		temporary bool
	}{
		{name: "identity-page-loses-quorum", fault: "identity-page", want: errRecoveryProbeIncomplete, temporary: true},
		{name: "frontier-loses-quorum", fault: "frontier", want: errRecoveryQuorumUnavailable, temporary: true},
		{name: "invalid-proof-is-permanent", fault: "invalid-proof", want: ch.ErrLogConflict},
		{name: "local-storage-error-is-preserved", fault: "local-storage", want: storageErr},
		{name: "invalid-authority-is-permanent", fault: "invalid-authority", want: ch.ErrInvalidConfig},
		{name: "caller-cancellation-is-preserved", fault: "canceled", want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			harness := &recoveryFailureHarness{replicaHarness: newReplicaHarness(t, 1, 2, 3), fault: test.fault, localError: storageErr}
			authority := Authority{
				Key: "1:recovery-failure", ChannelID: ch.ChannelID{ID: "recovery-failure", Type: 1},
				ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
				Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
			}
			original, _ := recoveryMutationAfter(t, authority.Key, authority.ChannelID, 41, 0, ch.EntryIdentity{})
			original.Committed = 1
			before := make(map[ch.NodeID]ReplicaState)
			for _, voter := range authority.Voters {
				seeded := harness.stores[voter].Sync(ctx, []Mutation{original})
				if len(seeded) != 1 || !seeded[0].Outcome.Durable() || seeded[0].Err != nil {
					t.Fatalf("seed voter %d: %+v", voter, seeded)
				}
				loaded, err := loadRecoveryReplicaState(ctx, harness.stores[voter], authority.Key, authority.ChannelID, nil)
				if err != nil {
					t.Fatal(err)
				}
				before[voter] = loaded.State
			}
			log, err := newQuorumLog(quorumLogConfig{
				Local: 1, Store: harness.stores[1], Recovery: harness, Durability: harness,
				RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10,
				MaxChannels: 8, MaxVoters: 3, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16,
			})
			if err != nil {
				t.Fatal(err)
			}
			attemptAuthority := authority
			attemptContext := ctx
			switch test.fault {
			case "invalid-authority":
				attemptAuthority.Voters = []ch.NodeID{1, 1, 3}
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				attemptContext = canceled
			}
			installed, installErr := log.Install(attemptContext, attemptAuthority)
			if !errors.Is(installErr, test.want) {
				t.Fatalf("Install() error = %v, want original cause %v", installErr, test.want)
			}
			if errors.Is(installErr, ch.ErrNotReady) != test.temporary {
				t.Errorf("Install() error = %v, retryable = %v; want %v", installErr, errors.Is(installErr, ch.ErrNotReady), test.temporary)
			}
			if installed != (Installed{}) {
				t.Fatalf("failed Install granted frontier: %+v", installed)
			}
			if state := log.channels[authority.Key]; state != nil && (state.ready || state.hw != 0 || state.frontier != (ReplicaState{})) {
				t.Fatalf("failed Install made state readable/writable: %+v", state)
			}
			next := Proposal{
				Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{31: 42},
				Records: []ch.Record{{ID: 42, Epoch: 3, FromUID: "sender", ClientMsgNo: "after-recovery",
					Payload: []byte("next"), SizeBytes: 4, ServerTimestampMS: 42}},
			}
			if test.temporary {
				if harness.blocked != 2 {
					t.Fatalf("blocked probes = %d, want both remote voters in the selected round", harness.blocked)
				}
				if _, err := log.Commit(ctx, next); !errors.Is(err, ch.ErrNotReady) {
					t.Fatalf("Commit before completed recovery = %v, want not ready", err)
				}
			}
			for _, voter := range authority.Voters {
				loaded, err := loadRecoveryReplicaState(ctx, harness.stores[voter], authority.Key, authority.ChannelID, nil)
				if err != nil || loaded.State != before[voter] {
					t.Fatalf("failed recovery changed voter %d: %+v, %v; want %+v", voter, loaded.State, err, before[voter])
				}
			}
			if harness.syncCalls != 0 {
				t.Fatalf("failed recovery dispatched %d durability writes", harness.syncCalls)
			}
			if !test.temporary {
				return
			}

			// Clearing only the probe fault must recover the acknowledged prefix
			// before any business append can warm the Channel.
			harness.fault = ""
			installed, err = log.Install(ctx, authority)
			if err != nil || installed != (Installed{Authority: authority.ID, LEO: 1, HW: 1}) {
				t.Fatalf("Install after probe recovery = %+v, %v; want exact original frontier", installed, err)
			}
			if harness.syncCalls != 0 {
				t.Fatalf("same-authority recovery dispatched %d unnecessary writes", harness.syncCalls)
			}
			expectedRecords := cloneRecords(original.Records)
			expectedRecords[0].Index = 1
			for _, voter := range authority.Voters {
				found := harness.stores[voter].(commandStore).LookupCommands(ctx, []CommandLookup{{
					ChannelKey: authority.Key, ChannelID: authority.ChannelID, CommandID: original.Manifest.CommandID,
					MaxRecords: 256, MaxBytes: 64 << 10,
				}})
				if len(found) != 1 || found[0].Err != nil || !found[0].Found ||
					found[0].Manifest != original.Manifest || !reflect.DeepEqual(found[0].Records, expectedRecords) {
					t.Fatalf("voter %d lost original content after recovery: %+v", voter, found)
				}
			}
			receipt, err := log.Commit(ctx, next)
			if err != nil || receipt.First != 2 || receipt.Last != 2 || receipt.HW != 2 {
				t.Fatalf("first append after recovery = %+v, %v; want next sequence 2", receipt, err)
			}
		})
	}
}

// recoveryFailureHarness changes only probe delivery/proof validity; every
// durable row and successful recovery operation uses the real replica adapter.
type recoveryFailureHarness struct {
	*replicaHarness
	fault      string
	blocked    int
	localError error
}

func (h *recoveryFailureHarness) submitRecoveryProbe(ctx context.Context, query recoveryProbeQuery, complete func(ProbeResult, error)) error {
	if h.fault == "local-storage" && query.Voter == query.Leader {
		complete(ProbeResult{}, h.localError)
		return nil
	}
	if query.Voter != query.Leader &&
		(h.fault == "frontier" || h.fault == "identity-page" && len(query.Indexes) > 0) {
		h.blocked++
		complete(ProbeResult{}, errPeerOutcomeUnknown)
		return nil
	}
	return h.replicaHarness.submitRecoveryProbe(ctx, query, func(result ProbeResult, err error) {
		if h.fault == "invalid-proof" && query.Voter == query.Leader && err == nil {
			result.State.Committed = result.State.LEO + 1
		}
		complete(result, err)
	})
}
