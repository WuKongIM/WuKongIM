package replication_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

func TestStoreAdapterLoadsExactDurableTailAfterSync(t *testing.T) {
	t.Parallel()

	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory:       channelstore.NewMemoryFactory(),
		MaxBatchItems: 4,
		MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}

	key := ch.ChannelKey("1:load-exact-tail")
	id := ch.ChannelID{ID: "load-exact-tail", Type: 1}
	empty, err := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key,
		ChannelID:  id,
	}}})
	if err != nil {
		t.Fatalf("Load(empty) error = %v", err)
	}
	if len(empty.Items) != 1 || empty.Items[0].Err != nil || empty.Items[0].State != (replication.ReplicaState{}) {
		t.Fatalf("Load(empty) = %+v, want one empty state", empty)
	}

	records := []ch.Record{{
		ID: 17, Epoch: 3, FromUID: "sender", ClientMsgNo: "command-17",
		Payload: []byte("payload"), SizeBytes: len("payload"), ServerTimestampMS: time.Unix(1_700_000_000, 0).UnixMilli(),
	}}
	manifest, entries, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: ch.CommandID{31: 17}, LastOffset: 1,
	}, records)
	if !ok {
		t.Fatal("SealProposalManifest() failed")
	}
	results := adapter.Sync(context.Background(), []replication.Mutation{{
		ChannelKey: key,
		ChannelID:  id,
		Manifest:   manifest,
		Records:    records,
		Committed:  1,
	}})
	if len(results) != 1 || results[0].Err != nil || results[0].Outcome != ch.AppendOutcomeDurable {
		t.Fatalf("Sync() = %+v, want one durable result", results)
	}

	loaded, err := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key,
		ChannelID:  id,
	}}})
	if err != nil {
		t.Fatalf("Load(durable) error = %v", err)
	}
	want := replication.ReplicaState{
		LEO:          1,
		Committed:    1,
		Manifest:     manifest,
		TailIdentity: entries[0],
	}
	if len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State != want {
		t.Fatalf("Load(durable) = %+v, want %+v", loaded, want)
	}
}

func TestStoreAdapterLoadReturnsPositionAlignedRecoveryIdentities(t *testing.T) {
	t.Parallel()

	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	key := ch.ChannelKey("1:recovery-identities")
	id := ch.ChannelID{ID: "recovery-identities", Type: 1}
	first := sealedMutationAt(t, key, id, 111, 0, ch.EntryIdentity{})
	firstEntries, ok := ch.DeriveProposalEntries(first.Manifest, len(first.Records), func(index int) ch.Record { return first.Records[index] })
	if !ok {
		t.Fatal("DeriveProposalEntries(first) failed")
	}
	second := sealedMutationAt(t, key, id, 112, 1, firstEntries[0])
	secondEntries, ok := ch.DeriveProposalEntries(second.Manifest, len(second.Records), func(index int) ch.Record { return second.Records[index] })
	if !ok {
		t.Fatal("DeriveProposalEntries(second) failed")
	}
	results := adapter.Sync(context.Background(), []replication.Mutation{first, second})
	if len(results) != 2 || !results[0].Outcome.Durable() || !results[1].Outcome.Durable() {
		t.Fatalf("Sync() = %+v, want two durable proposals", results)
	}

	loaded, err := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id, ProbeIndexes: []uint64{1, 2, 3},
	}}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Items) != 1 || loaded.Items[0].Err != nil {
		t.Fatalf("Load() = %+v, want one successful result", loaded)
	}
	want := []replication.EntryProbe{
		{Index: 1, Present: true, Identity: firstEntries[0]},
		{Index: 2, Present: true, Identity: secondEntries[0]},
		{Index: 3},
	}
	if !reflect.DeepEqual(loaded.Items[0].Entries, want) {
		t.Fatalf("Load() entries = %+v, want %+v", loaded.Items[0].Entries, want)
	}
}

func TestStoreAdapterReplacesDivergentRecoverySuffixDurably(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	factory := channelstore.NewMessageDBFactory(dir)
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	key := ch.ChannelKey("1:replace-recovery-suffix")
	id := ch.ChannelID{ID: "replace-recovery-suffix", Type: 1}
	first := sealedMutationAt(t, key, id, 71, 0, ch.EntryIdentity{})
	first.Committed = 1
	firstEntries, ok := ch.DeriveProposalEntries(first.Manifest, len(first.Records), func(index int) ch.Record {
		return first.Records[index]
	})
	if !ok {
		t.Fatal("DeriveProposalEntries(first) failed")
	}
	divergent := sealedMutationAt(t, key, id, 72, 1, firstEntries[0])
	if results := adapter.Sync(context.Background(), []replication.Mutation{first, divergent}); len(results) != 2 || !results[0].Outcome.Durable() || !results[1].Outcome.Durable() {
		t.Fatalf("Sync(divergent) = %+v, want two durable proposals", results)
	}
	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id,
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil {
		t.Fatalf("Load(divergent) = %+v, error %v", loaded, loadErr)
	}

	replacement := sealedMutationAt(t, key, id, 73, 1, firstEntries[0])
	replaceResults := adapter.Replace(context.Background(), []replication.RecoveryReplacement{{
		ChannelKey:  key,
		ChannelID:   id,
		Expected:    loaded.Items[0].State,
		KeepThrough: 1,
		Proposals: []replication.RecoveryProposal{{
			Manifest: replacement.Manifest,
			Records:  replacement.Records,
		}},
		Committed: 1,
	}})
	if len(replaceResults) != 1 || replaceResults[0].Err != nil || replaceResults[0].Outcome != ch.AppendOutcomeDurable ||
		replaceResults[0].LastOffset != 2 {
		t.Fatalf("Replace() = %+v, want durable replacement at offset 2", replaceResults)
	}
	if err := factory.Close(); err != nil {
		t.Fatalf("MessageDBFactory.Close() error = %v", err)
	}

	reopened := channelstore.NewMessageDBFactory(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	adapter, err = replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: reopened, MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter(reopened) error = %v", err)
	}
	replacementEntries, ok := ch.DeriveProposalEntries(replacement.Manifest, len(replacement.Records), func(index int) ch.Record {
		return replacement.Records[index]
	})
	if !ok {
		t.Fatal("DeriveProposalEntries(replacement) failed")
	}
	loaded, loadErr = adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id, ProbeIndexes: []uint64{1, 2},
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil {
		t.Fatalf("Load(reopened replacement) = %+v, error %v", loaded, loadErr)
	}
	if got := loaded.Items[0].State; got.LEO != 2 || got.Committed != 1 || got.Manifest != replacement.Manifest ||
		got.TailIdentity != replacementEntries[0] {
		t.Fatalf("Load(reopened replacement) state = %+v, want replacement frontier", got)
	}
	if got := loaded.Items[0].Entries; len(got) != 2 || got[0].Identity != firstEntries[0] ||
		got[1].Identity != replacementEntries[0] {
		t.Fatalf("Load(reopened replacement) entries = %+v, want repaired prefix", got)
	}
}

func TestStoreAdapterRejectsOversizedRecoveryReplacementBeforeOpeningStore(t *testing.T) {
	t.Parallel()

	factory := &countingStoreFactory{store: &channelstore.MemoryChannelStore{}}
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 1, MaxBatchBytes: 512,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	key := ch.ChannelKey("1:oversized-recovery")
	id := ch.ChannelID{ID: "oversized-recovery", Type: 1}
	records := []ch.Record{{
		ID: 81, Epoch: 3, FromUID: "sender", ClientMsgNo: "oversized-recovery-81",
		Payload: make([]byte, 1024), SizeBytes: 1024,
		ServerTimestampMS: time.Unix(1_700_000_300, 0).UnixMilli(),
	}}
	manifest, _, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: ch.CommandID{31: 81}, LastOffset: 1,
	}, records)
	if !ok {
		t.Fatal("SealProposalManifest() failed")
	}
	results := adapter.Replace(context.Background(), []replication.RecoveryReplacement{{
		ChannelKey: key, ChannelID: id,
		Proposals: []replication.RecoveryProposal{{Manifest: manifest, Records: records}},
	}})
	if len(results) != 1 || results[0].Outcome != ch.AppendOutcomeDefinitelyNotWritten ||
		!errors.Is(results[0].Err, ch.ErrBackpressured) {
		t.Fatalf("Replace(oversized) = %+v, want pre-admission backpressure", results)
	}
	if factory.calls != 0 {
		t.Fatalf("ChannelStore() calls = %d, want zero before bounded admission", factory.calls)
	}
}

func TestStoreAdapterReplacesSuffixAfterPhysicalPrefixRetention(t *testing.T) {
	t.Parallel()

	factory := channelstore.NewMemoryFactory()
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	key := ch.ChannelKey("1:retained-recovery-prefix")
	id := ch.ChannelID{ID: "retained-recovery-prefix", Type: 1}
	first := sealedMutationAt(t, key, id, 91, 0, ch.EntryIdentity{})
	first.Committed = 1
	firstEntries, _ := ch.DeriveProposalEntries(first.Manifest, len(first.Records), func(index int) ch.Record { return first.Records[index] })
	second := sealedMutationAt(t, key, id, 92, 1, firstEntries[0])
	secondEntries, _ := ch.DeriveProposalEntries(second.Manifest, len(second.Records), func(index int) ch.Record { return second.Records[index] })
	divergent := sealedMutationAt(t, key, id, 93, 2, secondEntries[0])
	if results := adapter.Sync(context.Background(), []replication.Mutation{first, second, divergent}); len(results) != 3 ||
		!results[0].Outcome.Durable() || !results[1].Outcome.Durable() || !results[2].Outcome.Durable() {
		t.Fatalf("Sync() = %+v, want three durable proposals", results)
	}
	store, err := factory.ChannelStore(key, id)
	if err != nil {
		t.Fatalf("ChannelStore() error = %v", err)
	}
	if _, err := store.AdoptRetentionBoundary(context.Background(), 1, "delivery"); err != nil {
		t.Fatalf("AdoptRetentionBoundary() error = %v", err)
	}
	if result, err := store.TrimMessagesThrough(context.Background(), 1, channelstore.RetentionTrimOptions{}); err != nil || result.Deleted != 1 {
		t.Fatalf("TrimMessagesThrough() = %+v, error %v; want one retained-prefix row", result, err)
	}
	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id,
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil {
		t.Fatalf("Load() = %+v, error %v", loaded, loadErr)
	}
	replacement := sealedMutationAt(t, key, id, 94, 2, secondEntries[0])
	results := adapter.Replace(context.Background(), []replication.RecoveryReplacement{{
		ChannelKey: key, ChannelID: id, Expected: loaded.Items[0].State, KeepThrough: 2,
		Proposals: []replication.RecoveryProposal{{Manifest: replacement.Manifest, Records: replacement.Records}},
		Committed: 1,
	}})
	if len(results) != 1 || results[0].Err != nil || results[0].Outcome != ch.AppendOutcomeDurable {
		t.Fatalf("Replace() = %+v, want durable retained-prefix repair", results)
	}
	loaded, loadErr = adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id, ProbeIndexes: []uint64{1, 2, 3},
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil ||
		loaded.Items[0].Entries[0].Identity != firstEntries[0] || loaded.Items[0].Entries[1].Identity != secondEntries[0] {
		t.Fatalf("Load(repaired) = %+v, error %v; want retained identity prefix", loaded, loadErr)
	}
}

func TestStoreAdapterRejectsReplacementBelowAdoptedRetentionBoundary(t *testing.T) {
	t.Parallel()

	factory := channelstore.NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	key := ch.ChannelKey("1:retention-fenced-recovery")
	id := ch.ChannelID{ID: "retention-fenced-recovery", Type: 1}
	first := sealedMutationAt(t, key, id, 101, 0, ch.EntryIdentity{})
	first.Committed = 1
	firstEntries, _ := ch.DeriveProposalEntries(first.Manifest, len(first.Records), func(index int) ch.Record { return first.Records[index] })
	second := sealedMutationAt(t, key, id, 102, 1, firstEntries[0])
	secondEntries, _ := ch.DeriveProposalEntries(second.Manifest, len(second.Records), func(index int) ch.Record { return second.Records[index] })
	third := sealedMutationAt(t, key, id, 103, 2, secondEntries[0])
	if results := adapter.Sync(context.Background(), []replication.Mutation{first, second, third}); len(results) != 3 ||
		!results[0].Outcome.Durable() || !results[1].Outcome.Durable() || !results[2].Outcome.Durable() {
		t.Fatalf("Sync() = %+v, want three durable proposals", results)
	}
	store, err := factory.ChannelStore(key, id)
	if err != nil {
		t.Fatalf("ChannelStore() error = %v", err)
	}
	if _, err := store.AdoptRetentionBoundary(context.Background(), 2, "delivery"); err != nil {
		t.Fatalf("AdoptRetentionBoundary() error = %v", err)
	}
	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id,
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil {
		t.Fatalf("Load() = %+v, error %v", loaded, loadErr)
	}
	replacementSecond := sealedMutationAt(t, key, id, 104, 1, firstEntries[0])
	replacementSecondEntries, _ := ch.DeriveProposalEntries(
		replacementSecond.Manifest, len(replacementSecond.Records), func(index int) ch.Record { return replacementSecond.Records[index] },
	)
	replacementThird := sealedMutationAt(t, key, id, 105, 2, replacementSecondEntries[0])
	results := adapter.Replace(context.Background(), []replication.RecoveryReplacement{{
		ChannelKey: key, ChannelID: id, Expected: loaded.Items[0].State, KeepThrough: 1,
		Proposals: []replication.RecoveryProposal{
			{Manifest: replacementSecond.Manifest, Records: replacementSecond.Records},
			{Manifest: replacementThird.Manifest, Records: replacementThird.Records},
		},
		Committed: 1,
	}})
	if len(results) != 1 || results[0].Outcome != ch.AppendOutcomeConflict || !errors.Is(results[0].Err, ch.ErrLogConflict) {
		t.Fatalf("Replace(below retention) = %+v, want conflict", results)
	}
	loadedAfter, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id,
	}}})
	if loadErr != nil || len(loadedAfter.Items) != 1 || loadedAfter.Items[0].Err != nil || loadedAfter.Items[0].State != loaded.Items[0].State {
		t.Fatalf("Load(after rejected replacement) = %+v, error %v; want unchanged %+v", loadedAfter, loadErr, loaded.Items[0].State)
	}
}

func TestStoreAdapterFetchesCompleteRecoveryProposalsFromOneView(t *testing.T) {
	t.Parallel()

	factory := channelstore.NewMemoryFactory()
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	key := ch.ChannelKey("1:fetch-recovery-proposals")
	id := ch.ChannelID{ID: "fetch-recovery-proposals", Type: 1}
	first := sealedMutationAt(t, key, id, 1111, 0, ch.EntryIdentity{})
	first.Committed = 1
	firstEntries, _ := ch.DeriveProposalEntries(first.Manifest, len(first.Records), func(index int) ch.Record { return first.Records[index] })
	second := sealedMutationAt(t, key, id, 1112, 1, firstEntries[0])
	if results := adapter.Sync(context.Background(), []replication.Mutation{first, second}); len(results) != 2 ||
		!results[0].Outcome.Durable() || !results[1].Outcome.Durable() {
		t.Fatalf("Sync() = %+v, want two durable proposals", results)
	}
	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id,
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil {
		t.Fatalf("Load() = %+v, error %v", loaded, loadErr)
	}
	results := adapter.Fetch(context.Background(), []replication.FetchRange{{
		ChannelKey: key, ChannelID: id, Expected: loaded.Items[0].State,
		From: 1, Through: 2, MaxBytes: 32 << 10,
	}})
	if len(results) != 1 || results[0].Err != nil || results[0].State != loaded.Items[0].State {
		t.Fatalf("Fetch() = %+v, want exact donor view", results)
	}
	want := []replication.RecoveryProposal{
		{Manifest: first.Manifest, Records: first.Records},
		{Manifest: second.Manifest, Records: second.Records},
	}
	want[0].Records[0].Index = 1
	want[1].Records[0].Index = 2
	if !reflect.DeepEqual(results[0].Proposals, want) {
		t.Fatalf("Fetch() proposals = %+v, want %+v", results[0].Proposals, want)
	}
}

func TestStoreAdapterFetchStopsAtLastCompleteProposalBeforePageCap(t *testing.T) {
	t.Parallel()

	factory := channelstore.NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 2, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	key := ch.ChannelKey("1:fetch-complete-cap")
	id := ch.ChannelID{ID: "fetch-complete-cap", Type: 1}
	firstRecords := []ch.Record{
		{ID: 1201, Epoch: 3, FromUID: "sender", ClientMsgNo: "first-1", Payload: []byte("one"), SizeBytes: 3, ServerTimestampMS: 1},
		{ID: 1202, Epoch: 3, FromUID: "sender", ClientMsgNo: "first-2", Payload: []byte("two"), SizeBytes: 3, ServerTimestampMS: 2},
	}
	firstManifest, firstEntries, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: ch.CommandID{31: 1}, BaseOffset: 0, LastOffset: 2,
	}, firstRecords)
	if !ok {
		t.Fatal("SealProposalManifest(first) failed")
	}
	secondRecords := []ch.Record{
		{ID: 1203, Epoch: 3, FromUID: "sender", ClientMsgNo: "second-1", Payload: []byte("three"), SizeBytes: 5, ServerTimestampMS: 3},
		{ID: 1204, Epoch: 3, FromUID: "sender", ClientMsgNo: "second-2", Payload: []byte("four"), SizeBytes: 4, ServerTimestampMS: 4},
	}
	secondManifest, _, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: ch.CommandID{31: 2}, BaseOffset: 2, LastOffset: 4,
		PreviousTerm: firstEntries[1].LeaderTerm, PreviousIndex: 2, PreviousDigest: firstEntries[1].Digest,
	}, secondRecords)
	if !ok {
		t.Fatal("SealProposalManifest(second) failed")
	}
	mutations := []replication.Mutation{
		{ChannelKey: key, ChannelID: id, Manifest: firstManifest, Records: firstRecords, Committed: 2},
		{ChannelKey: key, ChannelID: id, Manifest: secondManifest, Records: secondRecords, Committed: 2},
	}
	if results := adapter.Sync(context.Background(), mutations); len(results) != 2 || !results[0].Outcome.Durable() || !results[1].Outcome.Durable() {
		t.Fatalf("Sync() = %+v, want two durable proposals", results)
	}
	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{ChannelKey: key, ChannelID: id}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil {
		t.Fatalf("Load() = %+v, error %v", loaded, loadErr)
	}
	fetched := adapter.Fetch(context.Background(), []replication.FetchRange{{
		ChannelKey: key, ChannelID: id, Expected: loaded.Items[0].State,
		From: 1, Through: 3, MaxBytes: 32 << 10,
	}})
	if len(fetched) != 1 || fetched[0].Err != nil || len(fetched[0].Proposals) != 1 ||
		fetched[0].Proposals[0].Manifest != firstManifest || len(fetched[0].Proposals[0].Records) != 2 {
		t.Fatalf("Fetch(cap inside second proposal) = %+v, want first complete proposal only", fetched)
	}
}

func TestStoreAdapterSyncUsesOneMessageDBBatch(t *testing.T) {
	t.Parallel()

	observer := &recordingCommitObserver{}
	factory := channelstore.NewMessageDBFactoryWithOptions(t.TempDir(), channelstore.MessageDBFactoryOptions{
		CommitObserver: observer,
	})
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	mutations := []replication.Mutation{
		sealedMutation(t, ch.ChannelKey("1:batch-a"), ch.ChannelID{ID: "batch-a", Type: 1}, 31),
		sealedMutation(t, ch.ChannelKey("1:batch-b"), ch.ChannelID{ID: "batch-b", Type: 1}, 32),
	}
	results := adapter.Sync(context.Background(), mutations)
	if len(results) != 2 {
		t.Fatalf("Sync() result count = %d, want 2", len(results))
	}
	for index, result := range results {
		if result.Err != nil || result.Outcome != ch.AppendOutcomeDurable {
			t.Fatalf("Sync()[%d] = %+v, want durable", index, result)
		}
	}
	events := observer.snapshot()
	if len(events) != 1 || events[0].Requests != 1 || events[0].Records != 2 {
		t.Fatalf("commit events = %+v, want one adapter-request/2-record physical commit", events)
	}
}

func TestStoreAdapterRoutesReplicaMutationClassesToDistinctCommitLanes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		class replication.MutationClass
		lane  string
	}{
		{name: "follower_quorum", class: replication.MutationClassFollowerQuorum, lane: "replica_foreground"},
		{name: "trailing", class: replication.MutationClassTrailing, lane: "replica_trailing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer := &recordingCommitObserver{}
			factory := channelstore.NewMessageDBFactoryWithOptions(t.TempDir(), channelstore.MessageDBFactoryOptions{
				CommitObserver: observer,
			})
			t.Cleanup(func() { _ = factory.Close() })
			adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
				Factory: factory, MaxBatchItems: 1, MaxBatchBytes: 64 << 10,
			})
			if err != nil {
				t.Fatalf("NewStoreAdapter() error = %v", err)
			}
			mutation := sealedMutation(t, ch.ChannelKey("1:"+tc.name), ch.ChannelID{ID: tc.name, Type: 1}, 41)
			mutation.Class = tc.class
			results := adapter.Sync(context.Background(), []replication.Mutation{mutation})
			if len(results) != 1 || results[0].Err != nil || !results[0].Outcome.Durable() {
				t.Fatalf("Sync() = %+v, want one durable result", results)
			}
			if lanes := observer.requestLaneSnapshot(); !reflect.DeepEqual(lanes, []string{tc.lane}) {
				t.Fatalf("commit request lanes = %v, want [%s]", lanes, tc.lane)
			}
		})
	}
}

func TestStoreAdapterSyncsAdjacentSameChannelMutationsInOneMessageDBBatch(t *testing.T) {
	t.Parallel()

	observer := &recordingCommitObserver{}
	factory := channelstore.NewMessageDBFactoryWithOptions(t.TempDir(), channelstore.MessageDBFactoryOptions{
		CommitObserver: observer,
	})
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	key := ch.ChannelKey("1:adjacent-batch")
	id := ch.ChannelID{ID: "adjacent-batch", Type: 1}
	first := sealedMutationAt(t, key, id, 51, 0, ch.EntryIdentity{})
	first.Committed = 1
	second := sealedMutationAt(t, key, id, 52, 1, ch.EntryIdentity{
		LeaderTerm: first.Manifest.LeaderTerm, Index: first.Manifest.LastOffset, Digest: first.Manifest.Digest,
	})
	second.Committed = 2

	results := adapter.Sync(context.Background(), []replication.Mutation{first, second})
	if len(results) != 2 {
		t.Fatalf("Sync() result count = %d, want 2", len(results))
	}
	for index, result := range results {
		if result.Err != nil || result.Outcome != ch.AppendOutcomeDurable || result.LastOffset != uint64(index+1) {
			t.Fatalf("Sync()[%d] = %+v, want durable offset %d", index, result, index+1)
		}
	}
	if events := observer.snapshot(); len(events) != 1 || events[0].Requests != 1 || events[0].Records != 2 {
		t.Fatalf("commit events = %+v, want one physical commit for two adjacent proposals", events)
	}
	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id,
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil ||
		loaded.Items[0].State.LEO != 2 || loaded.Items[0].State.Committed != 2 ||
		loaded.Items[0].State.Manifest != second.Manifest {
		t.Fatalf("Load() = %+v, error %v; want second exact proposal committed", loaded, loadErr)
	}
	replayed := adapter.Sync(context.Background(), []replication.Mutation{first, second})
	for index, result := range replayed {
		if result.Err != nil || result.Outcome != ch.AppendOutcomeAlreadyDurable || result.LastOffset != uint64(index+1) {
			t.Fatalf("Sync(replay)[%d] = %+v, want already durable offset %d", index, result, index+1)
		}
	}
	if events := observer.snapshot(); len(events) != 1 {
		t.Fatalf("commit events after replay = %+v, want no second physical commit", events)
	}
}

func TestStoreAdapterReturnsNeedFromForFollowerGap(t *testing.T) {
	t.Parallel()

	factory := channelstore.NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 2, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	mutation := sealedMutationAt(t, ch.ChannelKey("1:gap"), ch.ChannelID{ID: "gap", Type: 1}, 61, 1, ch.EntryIdentity{
		LeaderTerm: 7, Index: 1, Digest: ch.EntryDigest{31: 1},
	})
	results := adapter.Sync(context.Background(), []replication.Mutation{mutation})
	if len(results) != 1 || results[0].Outcome != ch.AppendOutcomeConflict ||
		!errors.Is(results[0].Err, ch.ErrLogConflict) || results[0].NeedFrom != 1 {
		t.Fatalf("Sync(gap) = %+v, want conflict with NeedFrom=1", results)
	}
}

func TestStoreAdapterLoadsExactDurableTailAfterMessageDBReopen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	observer := &recordingCommitObserver{}
	factory := channelstore.NewMessageDBFactoryWithOptions(dir, channelstore.MessageDBFactoryOptions{CommitObserver: observer})
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	key := ch.ChannelKey("1:reopen-exact-tail")
	id := ch.ChannelID{ID: "reopen-exact-tail", Type: 1}
	records := []ch.Record{{
		ID: 23, Epoch: 4, FromUID: "sender", ClientMsgNo: "command-23",
		Payload: []byte("durable"), SizeBytes: len("durable"), ServerTimestampMS: time.Unix(1_700_000_100, 0).UnixMilli(),
	}}
	manifest, entries, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 4, LeaderTerm: 6, FenceVersion: 8,
		CommandID: ch.CommandID{31: 23}, LastOffset: 1,
	}, records)
	if !ok {
		t.Fatal("SealProposalManifest() failed")
	}
	results := adapter.Sync(context.Background(), []replication.Mutation{{
		ChannelKey: key, ChannelID: id, Manifest: manifest, Records: records, Committed: 1,
	}})
	if len(results) != 1 || results[0].Err != nil || results[0].Outcome != ch.AppendOutcomeDurable {
		t.Fatalf("Sync() = %+v, want one durable result", results)
	}
	if events := observer.snapshot(); len(events) != 1 || events[0].Records != 1 {
		t.Fatalf("commit events = %+v, want exact proposal and HW in one physical commit", events)
	}
	if err := factory.Close(); err != nil {
		t.Fatalf("MessageDBFactory.Close() error = %v", err)
	}

	reopened := channelstore.NewMessageDBFactory(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	adapter, err = replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: reopened, MaxBatchItems: 4, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter(reopened) error = %v", err)
	}
	loaded, err := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id, ProbeIndexes: []uint64{1, 2},
	}}})
	if err != nil {
		t.Fatalf("Load(reopened) error = %v", err)
	}
	want := replication.ReplicaState{LEO: 1, Committed: 1, Manifest: manifest, TailIdentity: entries[0]}
	if len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State != want {
		t.Fatalf("Load(reopened) = %+v, want %+v", loaded, want)
	}
	wantEntries := []replication.EntryProbe{{Index: 1, Present: true, Identity: entries[0]}, {Index: 2}}
	if !reflect.DeepEqual(loaded.Items[0].Entries, wantEntries) {
		t.Fatalf("Load(reopened) entries = %+v, want %+v", loaded.Items[0].Entries, wantEntries)
	}
}

func TestStoreAdapterRejectsCommittedFrontierBeyondProposalBeforeWrite(t *testing.T) {
	t.Parallel()

	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 2, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	mutation := sealedMutation(t, ch.ChannelKey("1:invalid-committed"), ch.ChannelID{ID: "invalid-committed", Type: 1}, 41)
	mutation.Committed = mutation.Manifest.LastOffset + 1
	results := adapter.Sync(context.Background(), []replication.Mutation{mutation})
	if len(results) != 1 || results[0].Outcome != ch.AppendOutcomeDefinitelyNotWritten || results[0].Err == nil {
		t.Fatalf("Sync(invalid committed) = %+v, want definitely-not-written error", results)
	}
	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: mutation.ChannelKey, ChannelID: mutation.ChannelID,
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State != (replication.ReplicaState{}) {
		t.Fatalf("Load(after rejected sync) = %+v, error %v; want empty state", loaded, loadErr)
	}
}

func TestStoreAdapterLoadRejectsCommittedFrontierBeyondDurableTail(t *testing.T) {
	t.Parallel()

	factory := channelstore.NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 2, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	mutation := sealedMutation(t, ch.ChannelKey("1:invalid-frontier"), ch.ChannelID{ID: "invalid-frontier", Type: 1}, 71)
	results := adapter.Sync(context.Background(), []replication.Mutation{mutation})
	if len(results) != 1 || results[0].Err != nil || !results[0].Outcome.Durable() {
		t.Fatalf("Sync() = %+v, want durable proposal", results)
	}
	store, err := factory.ChannelStore(mutation.ChannelKey, mutation.ChannelID)
	if err != nil {
		t.Fatalf("ChannelStore() error = %v", err)
	}
	if err := store.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 2}); err != nil {
		t.Fatalf("StoreCheckpoint(HW>LEO) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("ChannelStore.Close() error = %v", err)
	}

	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: mutation.ChannelKey, ChannelID: mutation.ChannelID,
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || !errors.Is(loaded.Items[0].Err, ch.ErrLogConflict) ||
		loaded.Items[0].State != (replication.ReplicaState{}) {
		t.Fatalf("Load(corrupt HW) = %+v, error %v; want zero state/log conflict", loaded, loadErr)
	}
}

func TestStoreAdapterNormalizesMalformedStoreProofs(t *testing.T) {
	t.Parallel()

	key := ch.ChannelKey("1:malformed-store")
	id := ch.ChannelID{ID: "malformed-store", Type: 1}
	malformed := &scriptedExactStore{
		state: channelstore.ExactState{InitialState: channelstore.InitialState{LEO: 1}},
		appendResult: channelstore.AppendLeaderResult{
			Outcome: ch.AppendOutcome(0), LastOffset: 1,
		},
	}
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: scriptedStoreFactory{store: malformed}, MaxBatchItems: 2, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id,
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].State != (replication.ReplicaState{}) || loaded.Items[0].Err == nil {
		t.Fatalf("Load(malformed) = %+v, error %v; want zero state/error", loaded, loadErr)
	}
	mutation := sealedMutation(t, key, id, 81)
	results := adapter.Sync(context.Background(), []replication.Mutation{mutation})
	if len(results) != 1 || results[0].Outcome != ch.AppendOutcomeUnknown || results[0].Err == nil {
		t.Fatalf("Sync(malformed proof) = %+v, want typed unknown/error", results)
	}
}

func TestStoreAdapterLoadRejectsOversizedIdentityBatch(t *testing.T) {
	t.Parallel()

	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 2, MaxBatchBytes: 1,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	_, err = adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: "1:oversized", ChannelID: ch.ChannelID{ID: "oversized", Type: 1},
	}}})
	if !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("Load(oversized) error = %v, want backpressured", err)
	}
}

func TestStoreAdapterLoadRejectsUnboundedRecoveryIdentityPage(t *testing.T) {
	t.Parallel()

	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 1, MaxBatchBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	indexes := make([]uint64, 257)
	for index := range indexes {
		indexes[index] = uint64(index + 1)
	}
	_, err = adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: "1:unbounded-probe", ChannelID: ch.ChannelID{ID: "unbounded-probe", Type: 1}, ProbeIndexes: indexes,
	}}})
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("Load(unbounded probe) error = %v, want invalid config", err)
	}
}

func TestStoreAdapterSyncRejectsCombinedBatchBytes(t *testing.T) {
	t.Parallel()

	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 2, MaxBatchBytes: 512,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	first := sealedMutation(t, "1:bytes-a", ch.ChannelID{ID: "bytes-a", Type: 1}, 91)
	second := sealedMutation(t, "1:bytes-b", ch.ChannelID{ID: "bytes-b", Type: 1}, 92)
	results := adapter.Sync(context.Background(), []replication.Mutation{first, second})
	if len(results) != 2 {
		t.Fatalf("Sync() result count = %d, want 2", len(results))
	}
	for index, result := range results {
		if result.Outcome != ch.AppendOutcomeDefinitelyNotWritten || !errors.Is(result.Err, ch.ErrBackpressured) {
			t.Fatalf("Sync()[%d] = %+v, want batch backpressure", index, result)
		}
	}
}

func TestStoreAdapterSharesOneCommitWithSameBatchExactReplay(t *testing.T) {
	t.Parallel()

	observer := &recordingCommitObserver{}
	factory := channelstore.NewMessageDBFactoryWithOptions(t.TempDir(), channelstore.MessageDBFactoryOptions{
		CommitObserver: observer,
	})
	t.Cleanup(func() { _ = factory.Close() })
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 2, MaxBatchBytes: 64 << 10,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	mutation := sealedMutation(t, "1:same-batch-replay", ch.ChannelID{ID: "same-batch-replay", Type: 1}, 101)
	mutation.Committed = 1
	results := adapter.Sync(context.Background(), []replication.Mutation{mutation, mutation})
	if len(results) != 2 || results[0].Outcome != ch.AppendOutcomeDurable ||
		results[1].Outcome != ch.AppendOutcomeAlreadyDurable {
		t.Fatalf("Sync(new,replay) = %+v, want durable/already-durable", results)
	}
	for index, result := range results {
		if result.Err != nil || result.LastOffset != 1 {
			t.Fatalf("Sync(new,replay)[%d] = %+v, want exact offset 1", index, result)
		}
	}
	if events := observer.snapshot(); len(events) != 1 || events[0].Records != 1 {
		t.Fatalf("commit events = %+v, want one physical row commit", events)
	}
}

func TestStoreAdapterLoadRejectsMemoryHWAboveLEO(t *testing.T) {
	t.Parallel()

	factory := channelstore.NewMemoryFactory()
	key := ch.ChannelKey("1:memory-invalid-frontier")
	id := ch.ChannelID{ID: "memory-invalid-frontier", Type: 1}
	store, err := factory.ChannelStore(key, id)
	if err != nil {
		t.Fatalf("ChannelStore() error = %v", err)
	}
	if err := store.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 1}); err != nil {
		t.Fatalf("StoreCheckpoint() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
		Factory: factory, MaxBatchItems: 1, MaxBatchBytes: 4096,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	loaded, loadErr := adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{{
		ChannelKey: key, ChannelID: id,
	}}})
	if loadErr != nil || len(loaded.Items) != 1 || !errors.Is(loaded.Items[0].Err, ch.ErrLogConflict) ||
		loaded.Items[0].State != (replication.ReplicaState{}) {
		t.Fatalf("Load(memory HW>LEO) = %+v, error %v; want zero state/log conflict", loaded, loadErr)
	}
}

func sealedMutation(t *testing.T, key ch.ChannelKey, id ch.ChannelID, messageID uint64) replication.Mutation {
	t.Helper()
	return sealedMutationAt(t, key, id, messageID, 0, ch.EntryIdentity{})
}

func sealedMutationAt(t *testing.T, key ch.ChannelKey, id ch.ChannelID, messageID, base uint64, previous ch.EntryIdentity) replication.Mutation {
	t.Helper()
	records := []ch.Record{{
		ID: messageID, Epoch: 3, FromUID: "sender", ClientMsgNo: id.ID + "-" + strconv.FormatUint(messageID, 10),
		Payload: []byte(id.ID), SizeBytes: len(id.ID), ServerTimestampMS: time.Unix(1_700_000_200, 0).UnixMilli(),
	}}
	commandID := ch.CommandID{}
	commandID[24] = byte(messageID >> 56)
	commandID[25] = byte(messageID >> 48)
	commandID[26] = byte(messageID >> 40)
	commandID[27] = byte(messageID >> 32)
	commandID[28] = byte(messageID >> 24)
	commandID[29] = byte(messageID >> 16)
	commandID[30] = byte(messageID >> 8)
	commandID[31] = byte(messageID)
	manifest, _, ok := ch.SealProposalManifest(ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: commandID, BaseOffset: base, LastOffset: base + 1,
		PreviousTerm: previous.LeaderTerm, PreviousIndex: base, PreviousDigest: previous.Digest,
	}, records)
	if !ok {
		t.Fatal("SealProposalManifest() failed")
	}
	return replication.Mutation{ChannelKey: key, ChannelID: id, Manifest: manifest, Records: records}
}

type scriptedStoreFactory struct {
	store channelstore.ChannelStore
}

func (f scriptedStoreFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (channelstore.ChannelStore, error) {
	return f.store, nil
}

type countingStoreFactory struct {
	store channelstore.ChannelStore
	calls int
}

func (f *countingStoreFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (channelstore.ChannelStore, error) {
	f.calls++
	return f.store, nil
}

type scriptedExactStore struct {
	channelstore.ChannelStore
	state        channelstore.ExactState
	loadErr      error
	appendResult channelstore.AppendLeaderResult
	appendErr    error
}

func (s *scriptedExactStore) LoadExactState(context.Context) (channelstore.ExactState, error) {
	return s.state, s.loadErr
}

func (s *scriptedExactStore) AppendLeader(context.Context, channelstore.AppendLeaderRequest) (channelstore.AppendLeaderResult, error) {
	return s.appendResult, s.appendErr
}

func (s *scriptedExactStore) Close() error { return nil }

type recordingCommitObserver struct {
	mu           sync.Mutex
	events       []messagedb.CommitCoordinatorBatchEvent
	requestLanes []string
}

func (o *recordingCommitObserver) SetCommitCoordinatorQueueDepth(int) {}

func (o *recordingCommitObserver) ObserveCommitCoordinatorBatch(event messagedb.CommitCoordinatorBatchEvent) {
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
}

func (o *recordingCommitObserver) ObserveCommitCoordinatorRequest(event messagedb.CommitCoordinatorRequestEvent) {
	o.mu.Lock()
	o.requestLanes = append(o.requestLanes, event.Lane)
	o.mu.Unlock()
}

func (o *recordingCommitObserver) snapshot() []messagedb.CommitCoordinatorBatchEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]messagedb.CommitCoordinatorBatchEvent(nil), o.events...)
}

func (o *recordingCommitObserver) requestLaneSnapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.requestLanes...)
}
