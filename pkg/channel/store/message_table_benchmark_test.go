package store

import (
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func BenchmarkMessageTableAppend(b *testing.B) {
	for _, rowsPerBatch := range []int{1, 32, 256} {
		b.Run(fmt.Sprintf("rows=%d", rowsPerBatch), func(b *testing.B) {
			st := newTestChannelStore(b)
			table := st.messageTable()
			rows := benchmarkMessageRows(st, rowsPerBatch)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				batch := st.engine.db.NewBatch()
				if err := table.append(batch, rows); err != nil {
					b.Fatalf("messageTable.append() error = %v", err)
				}
				if err := batch.Close(); err != nil {
					b.Fatalf("Batch.Close() error = %v", err)
				}
			}
		})
	}
}

func BenchmarkMessageTableScanBySeq(b *testing.B) {
	for _, rowsPerScan := range []int{1, 32, 256} {
		b.Run(fmt.Sprintf("forward/rows=%d", rowsPerScan), func(b *testing.B) {
			st := newBenchmarkMessageTableStore(b, rowsPerScan)
			table := st.messageTable()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rows, err := table.scanBySeq(1, rowsPerScan, 1<<30)
				if err != nil {
					b.Fatalf("scanBySeq() error = %v", err)
				}
				if len(rows) != rowsPerScan {
					b.Fatalf("scanBySeq() rows = %d, want %d", len(rows), rowsPerScan)
				}
			}
		})
		b.Run(fmt.Sprintf("reverse/rows=%d", rowsPerScan), func(b *testing.B) {
			st := newBenchmarkMessageTableStore(b, rowsPerScan)
			table := st.messageTable()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rows, err := table.scanBySeqReverse(uint64(rowsPerScan), rowsPerScan, 1<<30)
				if err != nil {
					b.Fatalf("scanBySeqReverse() error = %v", err)
				}
				if len(rows) != rowsPerScan {
					b.Fatalf("scanBySeqReverse() rows = %d, want %d", len(rows), rowsPerScan)
				}
			}
		})
	}
}

func newBenchmarkMessageTableStore(tb testing.TB, rows int) *ChannelStore {
	tb.Helper()

	st := newTestChannelStore(tb)
	records := make([]channel.Record, 0, rows)
	for _, row := range benchmarkMessageRows(st, rows) {
		record, err := row.toCompatibilityRecord()
		if err != nil {
			tb.Fatalf("toCompatibilityRecord() error = %v", err)
		}
		records = append(records, channel.Record{
			ID:        row.MessageID,
			Payload:   record.Payload,
			SizeBytes: record.SizeBytes,
		})
	}
	if _, err := st.Append(records); err != nil {
		tb.Fatalf("Append() error = %v", err)
	}
	return st
}

func benchmarkMessageRows(st *ChannelStore, rows int) []messageRow {
	out := make([]messageRow, 0, rows)
	for i := 0; i < rows; i++ {
		msg := channel.Message{
			MessageID:   uint64(i + 1),
			ClientMsgNo: fmt.Sprintf("client-%d", i+1),
			FromUID:     "u1",
			ChannelID:   st.id.ID,
			ChannelType: st.id.Type,
			Payload:     []byte(fmt.Sprintf("payload-%d", i+1)),
		}
		row := messageRowFromChannelMessage(msg)
		row.MessageSeq = uint64(i + 1)
		out = append(out, row)
	}
	return out
}
