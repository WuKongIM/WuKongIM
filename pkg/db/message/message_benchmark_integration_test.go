//go:build integration

package message

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func BenchmarkChannelLogAppend(b *testing.B) {
	for _, recordsPerAppend := range []int{1, 32, 256} {
		b.Run(fmt.Sprintf("records=%d", recordsPerAppend), func(b *testing.B) {
			log, closeFn := openBenchmarkLog(b, "bench-append")
			defer closeFn()
			benchmarkChannelLogAppend(b, log, appendBenchmarkConfig{
				RecordsPerAppend: recordsPerAppend,
				PayloadSize:      len("benchmark-payload"),
				Mode:             AppendStrict,
				Indexed:          true,
			})
		})
	}
}

func BenchmarkChannelLeaseSteadyAppend(b *testing.B) {
	db, closeDB := openBenchmarkDB(b)
	defer closeDB()
	log, err := db.Channel("bench-lease-steady:1", ChannelID{ID: "bench-lease-steady", Type: 1})
	if err != nil {
		b.Fatalf("Channel(): %v", err)
	}
	defer func() {
		if err := log.Close(); err != nil {
			b.Fatalf("ChannelLog.Close(): %v", err)
		}
	}()
	payload := bytes.Repeat([]byte{'p'}, 128)

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seq := uint64(i + 1)
		if _, err := log.Append(context.Background(), []Record{{ID: seq, Payload: payload}}, AppendOptions{}); err != nil {
			b.Fatalf("Append(): %v", err)
		}
	}
	b.StopTimer()
}

func BenchmarkChannelLeaseColdReacquire(b *testing.B) {
	db, closeDB := openBenchmarkDB(b)
	defer closeDB()
	key := ChannelKey("bench-lease-cold:1")
	id := ChannelID{ID: "bench-lease-cold", Type: 1}
	seed, err := db.Channel(key, id)
	if err != nil {
		b.Fatalf("seed Channel(): %v", err)
	}
	if _, err := seed.Append(context.Background(), []Record{{ID: 1, Payload: []byte("seed")}}, AppendOptions{}); err != nil {
		b.Fatalf("seed Append(): %v", err)
	}
	if err := seed.Close(); err != nil {
		b.Fatalf("seed ChannelLog.Close(): %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		log, err := db.Channel(key, id)
		if err != nil {
			b.Fatalf("Channel(): %v", err)
		}
		if leo, err := log.LEO(context.Background()); err != nil || leo != 1 {
			b.Fatalf("LEO() = %d, %v, want 1, nil", leo, err)
		}
		if err := log.Close(); err != nil {
			b.Fatalf("ChannelLog.Close(): %v", err)
		}
	}
	b.StopTimer()
	if snapshot := db.ChannelEntryMetricsSnapshot(); snapshot.ActiveEntries != 0 || snapshot.OutstandingLeases != 0 {
		b.Fatalf("registry retained cold leases: %+v", snapshot)
	}
}

func BenchmarkChannelLogAppendMatrix(b *testing.B) {
	cases := []appendBenchmarkConfig{
		{Name: "strict/indexed", Mode: AppendStrict, Indexed: true, PayloadSize: 128},
		{Name: "trusted/indexed", Mode: AppendTrustedContiguous, Indexed: true, PayloadSize: 128},
		{Name: "strict/no_idempotency", Mode: AppendStrict, Indexed: false, PayloadSize: 128},
	}
	for _, recordsPerAppend := range []int{1, 32, 256, 1024} {
		for _, tc := range cases {
			tc := tc
			tc.RecordsPerAppend = recordsPerAppend
			b.Run(fmt.Sprintf("%s/records=%d/payload=%dB", tc.Name, recordsPerAppend, tc.PayloadSize), func(b *testing.B) {
				log, closeFn := openBenchmarkLog(b, ChannelKey("bench-append-"+tc.Name+"-"+strconv.Itoa(recordsPerAppend)))
				defer closeFn()
				benchmarkChannelLogAppend(b, log, tc)
			})
		}
	}
}

func BenchmarkChannelLogAppendPayloadSize(b *testing.B) {
	for _, payloadSize := range []int{0, 128, 4 << 10, 64 << 10} {
		payloadSize := payloadSize
		b.Run(fmt.Sprintf("records=32/payload=%dB", payloadSize), func(b *testing.B) {
			log, closeFn := openBenchmarkLog(b, ChannelKey("bench-payload-"+strconv.Itoa(payloadSize)))
			defer closeFn()
			benchmarkChannelLogAppend(b, log, appendBenchmarkConfig{
				RecordsPerAppend: 32,
				PayloadSize:      payloadSize,
				Mode:             AppendStrict,
				Indexed:          true,
			})
		})
	}
}

func BenchmarkChannelLogAppendPreseeded(b *testing.B) {
	for _, preseed := range []int{0, 10_000, 100_000} {
		preseed := preseed
		b.Run(fmt.Sprintf("preseed=%d/records=32/payload=128B", preseed), func(b *testing.B) {
			log, closeFn := openBenchmarkLog(b, ChannelKey("bench-preseed-"+strconv.Itoa(preseed)))
			defer closeFn()
			seedBenchmarkMessagesSized(b, log, preseed, 128, true)
			benchmarkChannelLogAppend(b, log, appendBenchmarkConfig{
				RecordsPerAppend: 32,
				PayloadSize:      128,
				Mode:             AppendStrict,
				Indexed:          true,
				BaseID:           uint64(preseed) + 1,
			})
		})
	}
}

func BenchmarkChannelLogAppendParallel(b *testing.B) {
	log, closeFn := openBenchmarkLog(b, "bench-parallel")
	defer closeFn()
	var nextID atomic.Uint64
	payload := []byte("benchmark-payload")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := nextID.Add(1)
			_, err := log.Append(context.Background(), []Record{{
				ID:          id,
				ClientMsgNo: fmt.Sprintf("c-%020d", id),
				FromUID:     "bench-u1",
				Payload:     payload,
			}}, AppendOptions{})
			if err != nil {
				b.Fatalf("Append(): %v", err)
			}
		}
	})
}

func BenchmarkChannelLogAppendParallelManyChannels(b *testing.B) {
	for _, channels := range []int{16, 128} {
		channels := channels
		b.Run(fmt.Sprintf("channels=%d", channels), func(b *testing.B) {
			db, closeFn := openBenchmarkDB(b)
			defer closeFn()
			logs := make([]*ChannelLog, channels)
			for i := range logs {
				key := ChannelKey(fmt.Sprintf("bench-parallel-channel-%03d", i))
				var err error
				logs[i], err = db.Channel(key, ChannelID{ID: string(key), Type: 1})
				if err != nil {
					b.Fatalf("Channel(): %v", err)
				}
			}
			var nextID atomic.Uint64
			payload := bytes.Repeat([]byte{'p'}, 128)

			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					id := nextID.Add(1)
					log := logs[int(id%uint64(channels))]
					_, err := log.Append(context.Background(), []Record{{
						ID:      id,
						Payload: payload,
					}}, AppendOptions{})
					if err != nil {
						b.Fatalf("Append(): %v", err)
					}
				}
			})
			b.StopTimer()
			b.ReportMetric(1, "records/op")
		})
	}
}

func BenchmarkChannelStoreAppend(b *testing.B) {
	for _, recordsPerAppend := range []int{1, 32, 256} {
		recordsPerAppend := recordsPerAppend
		b.Run(fmt.Sprintf("records=%d", recordsPerAppend), func(b *testing.B) {
			eng, err := Open(b.TempDir())
			if err != nil {
				b.Fatalf("Open(): %v", err)
			}
			defer func() {
				if err := eng.Close(); err != nil {
					b.Fatalf("Close(): %v", err)
				}
			}()
			store := mustForChannel(b, eng, channel.ChannelKey("compat-append:1"), channel.ChannelID{ID: "compat-append", Type: 1})
			batches := makeBenchmarkCompatRecordBatches(b, b.N, recordsPerAppend, 1, "compat-append")
			payloadBytes := benchmarkCompatPayloadBytes(recordsPerAppend)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := store.Append(batches[i]); err != nil {
					b.Fatalf("Append(): %v", err)
				}
			}
			b.StopTimer()
			reportAppendMetrics(b, recordsPerAppend, payloadBytes)
		})
	}
}

func BenchmarkStoreAppendBatch(b *testing.B) {
	for _, channels := range []int{8, 32, 128} {
		channels := channels
		b.Run(fmt.Sprintf("channels=%d/records_per_channel=1", channels), func(b *testing.B) {
			eng, err := Open(b.TempDir())
			if err != nil {
				b.Fatalf("Open(): %v", err)
			}
			defer func() {
				if err := eng.Close(); err != nil {
					b.Fatalf("Close(): %v", err)
				}
			}()
			stores := make([]*ChannelStore, channels)
			for i := range stores {
				channelID := fmt.Sprintf("compat-batch-%03d", i)
				stores[i] = mustForChannel(b, eng, channel.ChannelKey(channelID+":1"), channel.ChannelID{ID: channelID, Type: 1})
			}
			batches := make([][]AppendBatchItem, b.N)
			nextID := uint64(1)
			for i := 0; i < b.N; i++ {
				items := make([]AppendBatchItem, channels)
				for j, store := range stores {
					channelID := fmt.Sprintf("compat-batch-%03d", j)
					items[j] = AppendBatchItem{
						Store:   store,
						Records: []channel.Record{benchmarkCompatRecord(nextID, channelID, benchmarkClientMsgNo(nextID))},
					}
					nextID++
				}
				batches[i] = items
			}
			payloadBytes := benchmarkCompatPayloadBytes(channels)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				results := StoreAppendBatch(context.Background(), batches[i])
				for j, result := range results {
					if result.Err != nil {
						b.Fatalf("StoreAppendBatch()[%d]: %v", j, result.Err)
					}
				}
			}
			b.StopTimer()
			reportAppendMetrics(b, channels, payloadBytes)
		})
	}
}

func BenchmarkExactAppendWarmWorkingSet(b *testing.B) {
	const (
		workingSet = 24 * 1024
		batchSize  = 128
	)
	eng, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open(): %v", err)
	}
	defer func() {
		if err := eng.Close(); err != nil {
			b.Fatalf("Close(): %v", err)
		}
	}()
	tails := make([][32]byte, workingSet)
	appendSweep := func(sweep int) {
		baseOffset := uint64(sweep)
		for start := 0; start < workingSet; start += batchSize {
			end := minInt(start+batchSize, workingSet)
			items := make([]AppendBatchItem, 0, end-start)
			stores := make([]*ChannelStore, 0, end-start)
			for index := start; index < end; index++ {
				channelID := "exact-warm-" + strconv.Itoa(index)
				store := mustForChannel(b, eng, channel.ChannelKey(channelID+":1"), channel.ChannelID{ID: channelID, Type: 1})
				stores = append(stores, store)
				messageID := uint64(sweep*workingSet + index + 1)
				record, err := compatibilityRecordFromRow(messageRow{
					MessageID: messageID, ClientMsgNo: benchmarkClientMsgNo(messageID), ChannelID: channelID,
					ChannelType: 1, FromUID: "bench-u1", ServerTimestampMS: 1_700_000_000_000 + int64(messageID),
					Payload: []byte("payload"),
				})
				if err != nil {
					b.Fatalf("compatibilityRecordFromRow(): %v", err)
				}
				record.Epoch = 5
				commandID := [32]byte{}
				binary.BigEndian.PutUint64(commandID[len(commandID)-8:], messageID)
				manifest := DurableProposalManifest{
					Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
					CommandID: commandID, BaseOffset: baseOffset, LastOffset: baseOffset + 1,
				}
				if baseOffset > 0 {
					manifest.PreviousTerm = 7
					manifest.PreviousIndex = baseOffset
					manifest.PreviousDigest = tails[index]
				}
				rows, err := compatibilityRowsFromRecords(baseOffset+1, []channel.Record{record})
				if err != nil {
					b.Fatalf("compatibilityRowsFromRecords(): %v", err)
				}
				entries, ok := deriveDurableProposalEntries(manifest, []channel.Record{record}, rows)
				if !ok || len(entries) != 1 {
					b.Fatal("deriveDurableProposalEntries() failed")
				}
				manifest.Digest = entries[0].Digest
				tails[index] = manifest.Digest
				items = append(items, AppendBatchItem{
					Store: store, Records: []channel.Record{record}, ExactBaseOffset: true,
					ExpectedBaseOffset: baseOffset, Proposal: manifest, ServerAllocatedMessageIDs: true,
				})
			}
			results := StoreAppendBatch(context.Background(), items)
			for index, result := range results {
				if result.Err != nil {
					b.Fatalf("StoreAppendBatch()[%d]: %v", index, result.Err)
				}
			}
			for _, store := range stores {
				if err := store.Close(); err != nil {
					b.Fatalf("ChannelStore.Close(): %v", err)
				}
			}
		}
	}

	appendSweep(0)
	validationsBefore := eng.db.durablePredecessorValidations.Load()
	b.ReportAllocs()
	b.ResetTimer()
	for sweep := 1; sweep <= b.N; sweep++ {
		appendSweep(sweep)
	}
	b.StopTimer()
	validations := eng.db.durablePredecessorValidations.Load() - validationsBefore
	if validations != 0 {
		b.Fatalf("durable predecessor validations = %d, want 0 for warm working set", validations)
	}
	b.ReportMetric(workingSet, "channels/op")
	b.ReportMetric(float64(validations)/float64(max(1, b.N)), "predecessor-validations/op")
}

// BenchmarkExactProposalReplay measures immutable retries through StoreAppendBatch.
// A genesis proposal isolates the two proposal-index reads plus one identity read per entry.
func BenchmarkExactProposalReplay(b *testing.B) {
	for _, recordsPerProposal := range []int{1, 32, 256} {
		recordsPerProposal := recordsPerProposal
		b.Run(fmt.Sprintf("records=%d", recordsPerProposal), func(b *testing.B) {
			eng, err := Open(b.TempDir())
			if err != nil {
				b.Fatalf("Open(): %v", err)
			}
			defer func() {
				if err := eng.Close(); err != nil {
					b.Fatalf("Close(): %v", err)
				}
			}()
			observer := &exactReplayCommitObserver{}
			eng.ConfigureCommitCoordinator(CommitCoordinatorConfig{Observer: observer})
			channelID := "exact-replay-" + strconv.Itoa(recordsPerProposal)
			store := mustForChannel(b, eng, channel.ChannelKey(channelID+":1"), channel.ChannelID{ID: channelID, Type: 1})
			defer func() {
				if err := store.Close(); err != nil {
					b.Fatalf("ChannelStore.Close(): %v", err)
				}
			}()

			item := makeBenchmarkExactReplayItem(b, store, channelID, recordsPerProposal)
			items := []AppendBatchItem{item}
			seed := StoreAppendBatch(context.Background(), items)
			if len(seed) != 1 || seed[0].Err != nil || seed[0].Outcome != quorumlog.AppendOutcomeDurable {
				b.Fatalf("seed StoreAppendBatch() = %+v, want durable", seed)
			}
			requestsBefore := observer.requests.Load()
			batchesBefore := observer.batches.Load()
			metricsBefore := eng.MetricsSnapshot()
			if requestsBefore != 1 || batchesBefore != 1 {
				b.Fatalf("seed commits: requests=%d batches=%d, want one each", requestsBefore, batchesBefore)
			}
			if metricsBefore.SequencedExactFreshAppends != 1 {
				b.Fatalf("seed sequenced fresh appends = %d, want 1", metricsBefore.SequencedExactFreshAppends)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				results := StoreAppendBatch(context.Background(), items)
				if len(results) != 1 || results[0].Err != nil || results[0].Outcome != quorumlog.AppendOutcomeAlreadyDurable {
					b.Fatalf("replay StoreAppendBatch() = %+v, want already durable", results)
				}
			}
			b.StopTimer()

			requests := observer.requests.Load() - requestsBefore
			batches := observer.batches.Load() - batchesBefore
			if requests != 0 || batches != 0 {
				b.Fatalf("replay submitted commits: requests=%d batches=%d, want zero", requests, batches)
			}
			metricsAfter := eng.MetricsSnapshot()
			if metricsAfter.SequencedExactFreshAppends != metricsBefore.SequencedExactFreshAppends {
				b.Fatalf("sequenced fresh appends changed from %d to %d during replay", metricsBefore.SequencedExactFreshAppends, metricsAfter.SequencedExactFreshAppends)
			}
			if metricsAfter.DurablePredecessorValidations != metricsBefore.DurablePredecessorValidations {
				b.Fatalf("predecessor validations changed from %d to %d during genesis replay", metricsBefore.DurablePredecessorValidations, metricsAfter.DurablePredecessorValidations)
			}
			if leo, err := store.LEOWithError(); err != nil || leo != uint64(recordsPerProposal) {
				b.Fatalf("LEOWithError() = (%d, %v), want (%d, nil)", leo, err, recordsPerProposal)
			}
			b.ReportMetric(float64(recordsPerProposal), "entries/op")
			b.ReportMetric(float64(recordsPerProposal+2), "contract-point-reads/op")
			b.ReportMetric(float64(requests)/float64(b.N), "commit-requests/op")
			b.ReportMetric(float64(batches)/float64(b.N), "physical-commits/op")
		})
	}
}

func BenchmarkChannelLogRead(b *testing.B) {
	log, closeFn := openBenchmarkLog(b, "bench-read")
	defer closeFn()
	seedBenchmarkMessages(b, log, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := log.Read(context.Background(), 1, ReadOptions{Limit: 100, MaxBytes: 1 << 20}); err != nil {
			b.Fatalf("Read(): %v", err)
		}
	}
}

func BenchmarkChannelLogReadReverseLimitOne(b *testing.B) {
	for _, history := range []int{100, 5000} {
		history := history
		b.Run(fmt.Sprintf("history=%d", history), func(b *testing.B) {
			log, closeFn := openBenchmarkLog(b, ChannelKey("bench-read-reverse-"+strconv.Itoa(history)))
			defer closeFn()
			seedBenchmarkMessagesSized(b, log, history, 128, false)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				messages, err := log.ReadReverse(context.Background(), uint64(history), ReadOptions{Limit: 1, MaxBytes: 1 << 20})
				if err != nil {
					b.Fatalf("ReadReverse(): %v", err)
				}
				if len(messages) != 1 || messages[0].MessageSeq != uint64(history) {
					b.Fatalf("ReadReverse() = %#v, want latest sequence %d", messages, history)
				}
			}
		})
	}
}

func BenchmarkChannelLogGetByMessageID(b *testing.B) {
	log, closeFn := openBenchmarkLog(b, "bench-id")
	defer closeFn()
	seedBenchmarkMessages(b, log, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := uint64(i%1000 + 1)
		if _, ok, err := log.GetByMessageID(context.Background(), id); err != nil || !ok {
			b.Fatalf("GetByMessageID(%d) = ok %v err %v", id, ok, err)
		}
	}
}

func BenchmarkChannelLogRetentionTrim(b *testing.B) {
	log, closeFn := openBenchmarkLog(b, "bench-retention")
	defer closeFn()
	payload := []byte("benchmark-payload")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seq := uint64(i + 1)
		if _, err := log.Append(context.Background(), []Record{{ID: seq, ClientMsgNo: fmt.Sprintf("c-%020d", seq), FromUID: "bench-u1", Payload: payload}}, AppendOptions{}); err != nil {
			b.Fatalf("Append(): %v", err)
		}
		if _, err := log.TrimPrefixThrough(context.Background(), seq); err != nil {
			b.Fatalf("TrimPrefixThrough(): %v", err)
		}
	}
}

func openBenchmarkLog(b *testing.B, key ChannelKey) (*ChannelLog, func()) {
	b.Helper()
	db, closeFn := openBenchmarkDB(b)
	log, err := db.Channel(key, ChannelID{ID: string(key), Type: 1})
	if err != nil {
		b.Fatalf("Channel(): %v", err)
	}
	return log, closeFn
}

func openBenchmarkDB(b *testing.B) (*MessageDB, func()) {
	b.Helper()
	eng, err := engine.Open(b.TempDir(), engine.Options{})
	if err != nil {
		b.Fatalf("engine.Open(): %v", err)
	}
	db := NewDB(eng)
	return db, func() {
		if err := db.Close(); err != nil {
			b.Fatalf("MessageDB.Close(): %v", err)
		}
	}
}

func seedBenchmarkMessages(b *testing.B, log *ChannelLog, count int) {
	b.Helper()
	records := makeBenchmarkRecords(1, count, []byte("benchmark-payload"), true)
	if _, err := log.Append(context.Background(), records, AppendOptions{}); err != nil {
		b.Fatalf("Append(): %v", err)
	}
}

// appendBenchmarkConfig describes one ChannelLog.Append benchmark input shape.
type appendBenchmarkConfig struct {
	// Name is the optional sub-benchmark label.
	Name string
	// RecordsPerAppend controls the logical batch size for each Append call.
	RecordsPerAppend int
	// PayloadSize controls the byte length of each record payload.
	PayloadSize int
	// Mode selects strict validation or trusted contiguous append behavior.
	Mode AppendMode
	// Indexed controls whether records include idempotency index fields.
	Indexed bool
	// BaseID is the first message ID used by generated benchmark records.
	BaseID uint64
}

// benchmarkChannelLogAppend runs Append with prebuilt records so setup cost does not dominate the timed section.
func benchmarkChannelLogAppend(b *testing.B, log *ChannelLog, cfg appendBenchmarkConfig) {
	b.Helper()
	if cfg.RecordsPerAppend <= 0 {
		b.Fatalf("RecordsPerAppend must be positive")
	}
	baseID := cfg.BaseID
	if baseID == 0 {
		baseID = 1
	}
	payload := bytes.Repeat([]byte{'p'}, cfg.PayloadSize)
	batches := make([][]Record, b.N)
	nextID := baseID
	for i := 0; i < b.N; i++ {
		batches[i] = makeBenchmarkRecords(nextID, cfg.RecordsPerAppend, payload, cfg.Indexed)
		nextID += uint64(cfg.RecordsPerAppend)
	}

	if cfg.PayloadSize > 0 {
		b.SetBytes(int64(cfg.RecordsPerAppend * cfg.PayloadSize))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := log.Append(context.Background(), batches[i], AppendOptions{Mode: cfg.Mode}); err != nil {
			b.Fatalf("Append(): %v", err)
		}
	}
	b.StopTimer()
	reportAppendMetrics(b, cfg.RecordsPerAppend, cfg.RecordsPerAppend*cfg.PayloadSize)
}

// makeBenchmarkRecords returns records with unique IDs and a shared immutable payload.
func makeBenchmarkRecords(baseID uint64, count int, payload []byte, indexed bool) []Record {
	records := make([]Record, count)
	for i := range records {
		id := baseID + uint64(i)
		records[i] = Record{ID: id, Payload: payload}
		if indexed {
			records[i].ClientMsgNo = benchmarkClientMsgNo(id)
			records[i].FromUID = "bench-u1"
		}
	}
	return records
}

// seedBenchmarkMessagesSized populates durable rows before benchmark timing starts.
func seedBenchmarkMessagesSized(b *testing.B, log *ChannelLog, count int, payloadSize int, indexed bool) {
	b.Helper()
	if count == 0 {
		return
	}
	payload := bytes.Repeat([]byte{'s'}, payloadSize)
	const batchSize = 1024
	for base := 1; base <= count; base += batchSize {
		n := minInt(batchSize, count-base+1)
		records := makeBenchmarkRecords(uint64(base), n, payload, indexed)
		if _, err := log.Append(context.Background(), records, AppendOptions{}); err != nil {
			b.Fatalf("seed Append(): %v", err)
		}
	}
}

// makeBenchmarkCompatRecordBatches prebuilds compatibility records outside the timed section.
func makeBenchmarkCompatRecordBatches(b *testing.B, batches int, recordsPerBatch int, baseID uint64, channelID string) [][]channel.Record {
	b.Helper()
	result := make([][]channel.Record, batches)
	nextID := baseID
	for i := 0; i < batches; i++ {
		records := make([]channel.Record, recordsPerBatch)
		for j := range records {
			records[j] = benchmarkCompatRecord(nextID, channelID, benchmarkClientMsgNo(nextID))
			nextID++
		}
		result[i] = records
	}
	return result
}

// benchmarkCompatRecord builds one legacy channel record with a durable message payload.
func benchmarkCompatRecord(messageID uint64, channelID string, clientMsgNo string) channel.Record {
	msg := channel.Message{
		MessageID:   messageID,
		ClientMsgNo: clientMsgNo,
		ChannelID:   channelID,
		ChannelType: 1,
		FromUID:     "bench-u1",
		Payload:     []byte("payload"),
	}
	payload := encodeBenchmarkCompatMessage(msg)
	return channel.Record{ID: messageID, Payload: payload, SizeBytes: len(payload)}
}

// encodeBenchmarkCompatMessage mirrors the compatibility durable message format used by ChannelStore.Append.
func encodeBenchmarkCompatMessage(msg channel.Message) []byte {
	payload := make([]byte, 0, channel.DurableMessageHeaderSize+64+len(msg.Payload))
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, msg.MessageID)
	payload = append(payload, 0, byte(msg.Setting), byte(msg.StreamFlag), msg.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, uint32(msg.Expire))
	payload = binary.BigEndian.AppendUint64(payload, msg.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, msg.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(msg.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, hashPayload(msg.Payload))
	payload = appendBenchmarkCompatString(payload, msg.MsgKey)
	payload = appendBenchmarkCompatString(payload, msg.ClientMsgNo)
	payload = appendBenchmarkCompatString(payload, msg.StreamNo)
	payload = appendBenchmarkCompatString(payload, msg.ChannelID)
	payload = appendBenchmarkCompatString(payload, msg.Topic)
	payload = appendBenchmarkCompatString(payload, msg.FromUID)
	payload = appendBenchmarkCompatBytes(payload, msg.Payload)
	return payload
}

func appendBenchmarkCompatString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendBenchmarkCompatBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func benchmarkClientMsgNo(id uint64) string {
	return "c-" + strconv.FormatUint(id, 10)
}

func reportAppendMetrics(b *testing.B, recordsPerAppend int, payloadBytesPerAppend int) {
	b.Helper()
	b.ReportMetric(float64(recordsPerAppend), "records/op")
	if recordsPerAppend > 0 {
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*recordsPerAppend), "record-ns/op")
	}
	if payloadBytesPerAppend > 0 {
		b.ReportMetric(float64(payloadBytesPerAppend), "payload-bytes/op")
	}
}

func benchmarkCompatPayloadBytes(records int) int {
	return records * len(benchmarkCompatRecord(1, "compat-size", "c-1").Payload)
}

func makeBenchmarkExactReplayItem(b *testing.B, store *ChannelStore, channelID string, recordCount int) AppendBatchItem {
	b.Helper()
	if recordCount <= 0 {
		b.Fatal("recordCount must be positive")
	}
	records := make([]channel.Record, recordCount)
	for index := range records {
		messageID := uint64(index + 1)
		record, err := compatibilityRecordFromRow(messageRow{
			MessageID: messageID, ClientMsgNo: benchmarkClientMsgNo(messageID), ChannelID: channelID,
			ChannelType: 1, FromUID: "bench-u1", ServerTimestampMS: 1_700_000_000_000 + int64(messageID),
			Payload: []byte("payload"),
		})
		if err != nil {
			b.Fatalf("compatibilityRecordFromRow(): %v", err)
		}
		record.Epoch = 5
		records[index] = record
	}
	commandID := quorumlog.CommandID{}
	binary.BigEndian.PutUint64(commandID[len(commandID)-8:], uint64(recordCount))
	manifest := DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: commandID, LastOffset: uint64(recordCount),
	}
	rows, err := compatibilityRowsFromRecords(1, records)
	if err != nil {
		b.Fatalf("compatibilityRowsFromRecords(): %v", err)
	}
	entries, ok := deriveDurableProposalEntries(manifest, records, rows)
	if !ok || len(entries) != recordCount {
		b.Fatal("deriveDurableProposalEntries() failed")
	}
	manifest.Digest = entries[len(entries)-1].Digest
	return AppendBatchItem{
		Store: store, Records: records, ExactBaseOffset: true, Proposal: manifest,
		ServerAllocatedMessageIDs: true,
	}
}

type exactReplayCommitObserver struct {
	requests atomic.Uint64
	batches  atomic.Uint64
}

func (*exactReplayCommitObserver) SetCommitCoordinatorQueueDepth(int) {}

func (o *exactReplayCommitObserver) ObserveCommitCoordinatorBatch(CommitCoordinatorBatchEvent) {
	o.batches.Add(1)
}

func (o *exactReplayCommitObserver) ObserveCommitCoordinatorRequest(CommitCoordinatorRequestEvent) {
	o.requests.Add(1)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
