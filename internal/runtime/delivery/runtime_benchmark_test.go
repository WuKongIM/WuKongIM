package delivery

import (
	"context"
	"fmt"
	"testing"
	"time"

	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

func BenchmarkRuntimePlanAdmissionSharedPayloadAllocation(b *testing.B) {
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		QueueSize:   1024,
		Workers:     4,
		Presence: planPresenceResolverFunc(func(context.Context, []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return []TargetPresenceResult{{}}
		}),
	})
	if err := runtime.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Stop(ctx); err != nil {
			b.Fatal(err)
		}
	}()
	plan := runtimePlanForTest(1)
	plan.Event.Payload = make([]byte, 4<<10)

	b.ReportAllocs()
	b.SetBytes(int64(len(plan.Event.Payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := runtime.EnqueueRecipientDeliveryPlan(context.Background(), plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimeExactTargetPresenceOwnerGroupingAndScheduling256(b *testing.B) {
	const count = 256
	plan := onlinedelivery.RecipientDeliveryPlan{
		Mode:    onlinedelivery.ModeDurable,
		Event:   channelappendcontract.CommittedEnvelope{MessageID: 1, MessageSeq: 1, Payload: make([]byte, 1024)},
		Targets: make([]onlinedelivery.RecipientTargetBatch, count),
	}
	results := make([]TargetPresenceResult, count)
	for i := 0; i < count; i++ {
		uid := fmt.Sprintf("u-%03d", i)
		plan.Targets[i] = onlinedelivery.RecipientTargetBatch{
			Target:     runtimeTargetForTest(uint16(i)),
			Recipients: []channelappendcontract.Recipient{{UID: uid}},
		}
		results[i] = TargetPresenceResult{Routes: []onlinedelivery.Route{{
			UID: uid, OwnerNodeID: uint64(i%4 + 1), OwnerBootID: 1,
			OwnerSeq: uint64(i + 1), SessionID: uint64(i + 1),
		}}}
	}
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 99,
		Presence: planPresenceResolverFunc(func(context.Context, []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			return results
		}),
		RemoteOwnerPusher: remoteOwnerPusherFunc(func(_ context.Context, push onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error) {
			return onlinedelivery.OwnerPushResult{Accepted: push.Routes}, nil
		}),
		OwnerPushBatchSize: 64,
		OwnerConcurrency:   4,
		RetryMaxAttempts:   1,
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := runtime.processPlan(context.Background(), plan); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAckTrackerBatchAcrossShards256(b *testing.B) {
	const count = 256
	tracker := NewAckTracker(AckTrackerOptions{ShardCount: 32})
	pending := make([]PendingRecvAck, count)
	indexes := make([]int, count)
	for i := range pending {
		pending[i] = PendingRecvAck{
			UID: fmt.Sprintf("u-%03d", i), SessionID: uint64(i + 1),
			MessageID: 1, MessageSeq: 1,
		}
		indexes[i] = i
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bind := tracker.BindBatch(pending)
		if bind.Bound != count || tracker.FinishBindBatch(pending, bind.Tokens, indexes) != count {
			b.Fatal("aligned ACK batch did not finish")
		}
		for _, item := range pending {
			if _, ok := tracker.Ack(Recvack{UID: item.UID, SessionID: item.SessionID, MessageID: item.MessageID}); !ok {
				b.Fatal("pending ACK missing")
			}
		}
	}
}

func BenchmarkRuntimeBounded100KRecipientWorkload(b *testing.B) {
	const total = 100_000
	const planSize = 256
	plans := make([]onlinedelivery.RecipientDeliveryPlan, 0, (total+planSize-1)/planSize)
	for offset := 0; offset < total; offset += planSize {
		end := offset + planSize
		if end > total {
			end = total
		}
		recipients := make([]channelappendcontract.Recipient, end-offset)
		for i := range recipients {
			recipients[i].UID = fmt.Sprintf("u-%06d", offset+i)
		}
		plans = append(plans, onlinedelivery.RecipientDeliveryPlan{
			Mode:  onlinedelivery.ModeDurable,
			Event: channelappendcontract.CommittedEnvelope{MessageID: uint64(len(plans) + 1)},
			Targets: []onlinedelivery.RecipientTargetBatch{{
				Target: runtimeTargetForTest(uint16(len(plans) % 256)), Recipients: recipients,
			}},
		})
	}
	empty := []TargetPresenceResult{{}}
	recipientBytes := 0
	runtime := NewRuntime(RuntimeOptions{
		LocalNodeID: 1,
		Presence: planPresenceResolverFunc(func(_ context.Context, targets []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult {
			for _, target := range targets {
				for _, recipient := range target.Recipients {
					recipientBytes += len(recipient.UID)
				}
			}
			return empty
		}),
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, plan := range plans {
			if err := runtime.processPlan(context.Background(), plan); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(total, "recipients/workload")
	b.ReportMetric(float64(recipientBytes)/float64(b.N), "recipient_bytes/workload")
}
