package replication

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestExchangeServerAcknowledgesOnlyExactDurableStoreResults(t *testing.T) {
	store := &recordingReplicaStore{results: []MutationResult{
		{Outcome: ch.AppendOutcomeDurable, LastOffset: 1},
		{Outcome: ch.AppendOutcomeAlreadyDurable, LastOffset: 1},
	}}
	server, err := NewExchangeServer(ExchangeServerConfig{LocalNode: 2, Store: store, MaxBatchItems: 4, MaxBatchBytes: 4096})
	if err != nil {
		t.Fatalf("NewExchangeServer() error = %v", err)
	}
	first := testReplicateRequest(t, "1:first", "first", 1, []byte("payload-a"))
	second := testReplicateRequest(t, "1:second", "second", 2, []byte("payload-b"))
	batch := ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{
		{RequestID: 11, Kind: ExchangeReplicate, Replicate: &first},
		{RequestID: 12, Kind: ExchangeReplicate, Replicate: &second},
	}}

	result, err := server.Handle(context.Background(), 1, batch)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(store.batches) != 1 || len(store.batches[0]) != 2 {
		t.Fatalf("store mutation batches = %v, want one batch of two", mutationBatchCounts(store.batches))
	}
	if got := string(store.batches[0][0].Records[0].Payload); got != "payload-a" {
		t.Fatalf("stored first payload = %q", got)
	}
	if result.Version != ExchangeVersion || len(result.Items) != 2 {
		t.Fatalf("Handle() result = %+v", result)
	}
	if result.Items[0].RequestID != 11 || result.Items[0].Replicate.Status != ReplicateDurable || result.Items[0].Replicate.LastOffset != 1 {
		t.Fatalf("first result = %+v", result.Items[0])
	}
	if result.Items[1].RequestID != 12 || result.Items[1].Replicate.Status != ReplicateAlreadyDurable || result.Items[1].Replicate.LastOffset != 1 {
		t.Fatalf("second result = %+v", result.Items[1])
	}
	if result.Items[0].Replicate.Proof != replicateProofFor(first) || result.Items[1].Replicate.Proof != replicateProofFor(second) {
		t.Fatalf("durable proofs = %+v, %+v, want exact request manifests", result.Items[0].Replicate.Proof, result.Items[1].Replicate.Proof)
	}
}

func TestExchangeServerRejectsInvalidManifestBeforeStoreMutation(t *testing.T) {
	store := &recordingReplicaStore{}
	server, err := NewExchangeServer(ExchangeServerConfig{LocalNode: 2, Store: store, MaxBatchItems: 4, MaxBatchBytes: 4096})
	if err != nil {
		t.Fatalf("NewExchangeServer() error = %v", err)
	}
	request := testReplicateRequest(t, "1:invalid", "invalid", 1, []byte("payload"))
	request.Manifest.Digest[0] ^= 0xff

	_, err = server.Handle(context.Background(), 1, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{{RequestID: 1, Kind: ExchangeReplicate, Replicate: &request}}})

	if err == nil {
		t.Fatal("Handle() error = nil, want invalid manifest rejection")
	}
	if len(store.batches) != 0 {
		t.Fatalf("store mutation batches = %d, want zero", len(store.batches))
	}
}

func TestExchangeServerMapsClosedStoreOutcomesWithoutErrorStrings(t *testing.T) {
	tests := []struct {
		name   string
		result MutationResult
		want   ReplicateResult
	}{
		{name: "need-from", result: MutationResult{Outcome: ch.AppendOutcomeConflict, NeedFrom: 1, Err: ch.ErrLogConflict}, want: ReplicateResult{Status: ReplicateNeedFrom, NeedFrom: 1}},
		{name: "conflict", result: MutationResult{Outcome: ch.AppendOutcomeConflict, Err: ch.ErrLogConflict}, want: ReplicateResult{Status: ReplicateConflict}},
		{name: "stale-fence", result: MutationResult{Outcome: ch.AppendOutcomeDefinitelyNotWritten, Err: ch.ErrStaleMeta}, want: ReplicateResult{Status: ReplicateStaleFence}},
		{name: "backpressured", result: MutationResult{Outcome: ch.AppendOutcomeDefinitelyNotWritten, Err: ch.ErrBackpressured}, want: ReplicateResult{Status: ReplicateBackpressured}},
		{name: "unknown", result: MutationResult{Outcome: ch.AppendOutcomeUnknown, Err: errors.New("lost result")}, want: ReplicateResult{Status: ReplicateOutcomeUnknown}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &recordingReplicaStore{results: []MutationResult{test.result}}
			server, err := NewExchangeServer(ExchangeServerConfig{LocalNode: 2, Store: store, MaxBatchItems: 1, MaxBatchBytes: 4096})
			if err != nil {
				t.Fatalf("NewExchangeServer() error = %v", err)
			}
			request := testReplicateRequest(t, "1:outcome", "outcome", 1, []byte("payload"))
			result, err := server.Handle(context.Background(), 1, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{{RequestID: 9, Kind: ExchangeReplicate, Replicate: &request}}})
			if err != nil {
				t.Fatalf("Handle() error = %v", err)
			}
			if len(result.Items) != 1 || result.Items[0].Replicate != test.want {
				t.Fatalf("Handle() result = %+v, want %+v", result, test.want)
			}
		})
	}
}

func TestExchangeServerRejectsOversizedBatchBeforeStoreMutation(t *testing.T) {
	store := &recordingReplicaStore{}
	request := testReplicateRequest(t, "1:oversized", "oversized", 1, []byte("payload"))
	server, err := NewExchangeServer(ExchangeServerConfig{
		LocalNode: 2, Store: store, MaxBatchItems: 1,
		MaxBatchBytes: estimateReplicateRequestBytes(request) - 1,
	})
	if err != nil {
		t.Fatalf("NewExchangeServer() error = %v", err)
	}

	_, err = server.Handle(context.Background(), 1, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{{RequestID: 1, Kind: ExchangeReplicate, Replicate: &request}}})

	if !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("Handle() error = %v, want backpressured", err)
	}
	if len(store.batches) != 0 {
		t.Fatalf("store batches = %d, want zero", len(store.batches))
	}
}

type recordingReplicaStore struct {
	results []MutationResult
	batches [][]Mutation
}

func (s *recordingReplicaStore) Sync(_ context.Context, mutations []Mutation) []MutationResult {
	s.batches = append(s.batches, append([]Mutation(nil), mutations...))
	return append([]MutationResult(nil), s.results...)
}

func mutationBatchCounts(batches [][]Mutation) []int {
	counts := make([]int, len(batches))
	for index := range batches {
		counts[index] = len(batches[index])
	}
	return counts
}
