package webhook

import (
	"context"
	"testing"
	"time"
)

func BenchmarkRuntimeNotifyAdmission(b *testing.B) {
	rt, err := New(RuntimeOptions{
		Sender:              discardSender{},
		QueueSize:           65536,
		Workers:             16,
		NotifyBatchMaxItems: 100,
		NotifyBatchMaxWait:  time.Millisecond,
		OnlineBatchMaxItems: 512,
		OnlineBatchMaxWait:  time.Millisecond,
		OfflineUIDBatchSize: 512,
		RequestTimeout:      time.Second,
		RetryMaxAttempts:    1,
	})
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		b.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rt.Stop(ctx); err != nil {
			b.Fatalf("Stop() error = %v", err)
		}
	}()

	msg := Message{
		MessageSeq:        1,
		ChannelID:         "bench-channel",
		ChannelType:       1,
		FromUID:           "bench-user",
		ClientMsgNo:       "bench-client-msg-no",
		ServerTimestampMS: time.Now().UnixMilli(),
		Payload:           []byte("benchmark webhook notify payload"),
		RedDot:            true,
		SyncOnce:          false,
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.MessageID = uint64(i + 1)
		rt.Notify(ctx, msg)
	}
}

type discardSender struct{}

func (discardSender) Send(context.Context, SendRequest) error {
	return nil
}
