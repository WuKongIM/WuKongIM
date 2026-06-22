package message

import (
	"context"
	"fmt"
	"testing"
)

var benchmarkMessageSendBatchSink []SendBatchItemResult

func BenchmarkMessageSendBatchNoHook(b *testing.B) {
	for _, count := range []int{1, 16, 128, 1024} {
		b.Run(fmt.Sprintf("items_%d", count), func(b *testing.B) {
			app := New(Options{Submitter: benchmarkMessageSubmitter{}})
			items := benchmarkMessageSendBatchItems(count)
			b.ReportAllocs()
			b.SetBytes(int64(count * len(items[0].Command.Payload)))
			for i := 0; i < b.N; i++ {
				benchmarkMessageSendBatchSink = app.SendBatch(items)
				if len(benchmarkMessageSendBatchSink) != count {
					b.Fatalf("results = %d, want %d", len(benchmarkMessageSendBatchSink), count)
				}
			}
		})
	}
}

func BenchmarkMessageSendBatchWithHook(b *testing.B) {
	for _, count := range []int{1, 16, 128, 1024} {
		b.Run(fmt.Sprintf("items_%d", count), func(b *testing.B) {
			app := New(Options{Submitter: benchmarkMessageSubmitter{}, SendHook: benchmarkMessageSendHook{}})
			items := benchmarkMessageSendBatchItems(count)
			b.ReportAllocs()
			b.SetBytes(int64(count * len(items[0].Command.Payload)))
			for i := 0; i < b.N; i++ {
				benchmarkMessageSendBatchSink = app.SendBatch(items)
				if len(benchmarkMessageSendBatchSink) != count {
					b.Fatalf("results = %d, want %d", len(benchmarkMessageSendBatchSink), count)
				}
			}
		})
	}
}

type benchmarkMessageSubmitter struct{}

func (benchmarkMessageSubmitter) Send(context.Context, SendCommand) (SendResult, error) {
	return SendResult{MessageID: 1, Reason: ReasonSuccess}, nil
}

func (benchmarkMessageSubmitter) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	results := make([]SendBatchItemResult, len(items))
	for i := range results {
		results[i] = SendBatchItemResult{Result: SendResult{MessageID: uint64(i + 1), Reason: ReasonSuccess}}
	}
	return results
}

type benchmarkMessageSendHook struct{}

func (benchmarkMessageSendHook) BeforeSend(_ context.Context, cmd SendCommand) (SendCommand, Reason, error) {
	return cmd, ReasonSuccess, nil
}

func benchmarkMessageSendBatchItems(count int) []SendBatchItem {
	items := make([]SendBatchItem, 0, count)
	for i := 0; i < count; i++ {
		items = append(items, SendBatchItem{Command: SendCommand{
			FromUID:     fmt.Sprintf("u%d", i),
			ChannelID:   "g1",
			ChannelType: channelTypeGroup,
			Payload:     []byte("hello"),
		}})
	}
	return items
}
