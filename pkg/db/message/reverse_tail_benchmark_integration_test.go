//go:build integration

package message

import (
	"context"
	"strings"
	"testing"
)

// BenchmarkReverseTailRead measures the bounded single-record preview path.
func BenchmarkReverseTailRead(b *testing.B) {
	s := openTestMessageStore(b)
	defer s.close(b)
	log := testChannelLog(s)
	if _, err := log.Append(context.Background(), testRecords(1, strings.Repeat("x", 256), strings.Repeat("y", 256), strings.Repeat("z", 256)), AppendOptions{}); err != nil {
		b.Fatal(err)
	}
	for _, from := range []struct {
		name string
		seq  uint64
	}{{"exact", 3}, {"missing_bound", 4}} {
		b.Run(from.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				rows, err := log.ReadReverse(context.Background(), from.seq, ReadOptions{Limit: 1, MaxBytes: 1 << 20})
				if err != nil || len(rows) != 1 || rows[0].MessageSeq != 3 {
					b.Fatalf("read = %v, %v", rows, err)
				}
			}
		})
	}
}
