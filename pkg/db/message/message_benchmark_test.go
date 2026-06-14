package message

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
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
				logs[i] = db.Channel(key, ChannelID{ID: string(key), Type: 1})
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
			store := eng.ForChannel(channel.ChannelKey("compat-append:1"), channel.ChannelID{ID: "compat-append", Type: 1})
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
				stores[i] = eng.ForChannel(channel.ChannelKey(channelID+":1"), channel.ChannelID{ID: channelID, Type: 1})
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
	return db.Channel(key, ChannelID{ID: string(key), Type: 1}), closeFn
}

func openBenchmarkDB(b *testing.B) (*MessageDB, func()) {
	b.Helper()
	eng, err := engine.Open(b.TempDir(), engine.Options{})
	if err != nil {
		b.Fatalf("engine.Open(): %v", err)
	}
	db := NewDB(eng)
	return db, func() {
		if err := eng.Close(); err != nil {
			b.Fatalf("engine.Close(): %v", err)
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
