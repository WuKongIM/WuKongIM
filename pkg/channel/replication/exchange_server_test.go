package replication

import (
	"context"
	"errors"
	"reflect"
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

func TestExchangeServerReturnsBoundedPositionAlignedRecoveryProbe(t *testing.T) {
	replicate := testReplicateRequest(t, "1:probe", "probe", 1, []byte("probe"))
	identities, ok := ch.DeriveProposalEntries(replicate.Manifest, len(replicate.Records), func(index int) ch.Record {
		return replicate.Records[index]
	})
	if !ok {
		t.Fatal("DeriveProposalEntries() failed")
	}
	identity := identities[0]
	store := &recordingReplicaStore{loadResult: LoadBatchResult{Items: []LoadResult{{
		State:   ReplicaState{LEO: 1, Committed: 1, Manifest: replicate.Manifest, TailIdentity: identity},
		Entries: []EntryProbe{{Index: 1, Present: true, Identity: identity}, {Index: 2}},
	}}}}
	server, err := NewExchangeServer(ExchangeServerConfig{LocalNode: 2, Store: store, MaxBatchItems: 4, MaxBatchBytes: 4096})
	if err != nil {
		t.Fatalf("NewExchangeServer() error = %v", err)
	}
	request := ProbeRequest{
		ChannelKey: "1:probe", ChannelID: ch.ChannelID{ID: "probe", Type: 1},
		Leader: 1, Follower: 2, Indexes: []uint64{1, 2},
	}

	result, err := server.Handle(context.Background(), 1, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{{
		RequestID: 21, Kind: ExchangeProbe, Probe: &request,
	}}})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(store.loadBatches) != 1 || len(store.loadBatches[0].Items) != 1 ||
		!reflect.DeepEqual(store.loadBatches[0].Items[0].ProbeIndexes, request.Indexes) {
		t.Fatalf("load batches = %+v, want one exact probe", store.loadBatches)
	}
	if len(store.batches) != 0 {
		t.Fatalf("mutation batches = %d, want read-only probe", len(store.batches))
	}
	if result.Version != ExchangeVersion || len(result.Items) != 1 || result.Items[0].RequestID != 21 ||
		!reflect.DeepEqual(result.Items[0].Probe, ProbeResult{Proof: probeProofFor(request), State: store.loadResult.Items[0].State, Entries: store.loadResult.Items[0].Entries}) {
		t.Fatalf("Handle() result = %+v, want position-aligned probe", result)
	}
}

func TestExchangeServerRejectsMixedReadWriteForSameChannelBeforeStoreAccess(t *testing.T) {
	store := &recordingReplicaStore{}
	server, err := NewExchangeServer(ExchangeServerConfig{LocalNode: 2, Store: store, MaxBatchItems: 4, MaxBatchBytes: 8192})
	if err != nil {
		t.Fatalf("NewExchangeServer() error = %v", err)
	}
	replicate := testReplicateRequest(t, "1:mixed", "mixed", 1, []byte("payload"))
	probe := ProbeRequest{
		ChannelKey: replicate.ChannelKey, ChannelID: replicate.ChannelID,
		Leader: replicate.Leader, Follower: replicate.Follower, Indexes: []uint64{1},
	}

	_, err = server.Handle(context.Background(), 1, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{
		{RequestID: 31, Kind: ExchangeReplicate, Replicate: &replicate},
		{RequestID: 32, Kind: ExchangeProbe, Probe: &probe},
	}})
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("Handle() error = %v, want invalid mixed same-Channel batch", err)
	}
	if len(store.loadBatches) != 0 || len(store.batches) != 0 {
		t.Fatalf("store access = loads %d writes %d, want zero", len(store.loadBatches), len(store.batches))
	}
}

func TestExchangeServerRejectsOversizedProbeBeforeStoreAccess(t *testing.T) {
	store := &recordingReplicaStore{}
	server, err := NewExchangeServer(ExchangeServerConfig{LocalNode: 2, Store: store, MaxBatchItems: 1, MaxBatchBytes: 200})
	if err != nil {
		t.Fatalf("NewExchangeServer() error = %v", err)
	}
	request := ProbeRequest{
		ChannelKey: "1:oversized-probe", ChannelID: ch.ChannelID{ID: "oversized-probe", Type: 1},
		Leader: 1, Follower: 2, Indexes: []uint64{1},
	}

	_, err = server.Handle(context.Background(), 1, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{{
		RequestID: 41, Kind: ExchangeProbe, Probe: &request,
	}}})
	if !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("Handle() error = %v, want backpressured", err)
	}
	if len(store.loadBatches) != 0 || len(store.batches) != 0 {
		t.Fatalf("store access = loads %d writes %d, want zero", len(store.loadBatches), len(store.batches))
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
	results     []MutationResult
	batches     [][]Mutation
	loadResult  LoadBatchResult
	loadBatches []LoadBatch
}

func (s *recordingReplicaStore) Load(_ context.Context, batch LoadBatch) (LoadBatchResult, error) {
	s.loadBatches = append(s.loadBatches, batch)
	if len(s.loadResult.Items) != 0 {
		return s.loadResult, nil
	}
	return LoadBatchResult{Items: make([]LoadResult, len(batch.Items))}, nil
}

func (s *recordingReplicaStore) Sync(_ context.Context, mutations []Mutation) []MutationResult {
	s.batches = append(s.batches, append([]Mutation(nil), mutations...))
	return append([]MutationResult(nil), s.results...)
}

func (*recordingReplicaStore) Replace(context.Context, []RecoveryReplacement) []RecoveryReplacementResult {
	return nil
}

func mutationBatchCounts(batches [][]Mutation) []int {
	counts := make([]int, len(batches))
	for index := range batches {
		counts[index] = len(batches[index])
	}
	return counts
}
