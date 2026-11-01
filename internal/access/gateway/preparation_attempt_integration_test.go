//go:build integration

package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type preparationEntrySubmitter struct{}

func (preparationEntrySubmitter) Send(context.Context, message.SendCommand) (message.SendResult, error) {
	return message.SendResult{Reason: message.ReasonSuccess, MessageID: 1, MessageSeq: 1}, nil
}
func (preparationEntrySubmitter) SendBatch(items []message.SendBatchItem) []message.SendBatchItemResult {
	results := make([]message.SendBatchItemResult, len(items))
	for i := range results {
		results[i].Result = message.SendResult{Reason: message.ReasonSuccess, MessageID: uint64(i + 1), MessageSeq: uint64(i + 1)}
	}
	return results
}

func TestAttemptPreparationGatewayToRealMessageAndFixedSummary(t *testing.T) {
	store := diagnostics.NewAttemptStore(diagnostics.AttemptStoreOptions{Enabled: true, NodeID: 1, Epoch: [16]byte{7}})
	// No general diagnostic event ring is needed for these original-context facts.
	restore := sendtrace.SetSink(diagnostics.NewSendTraceSink(nil, nil).WithAttemptStore(store))
	defer restore()
	var written []frame.Frame
	sess := newTestSession(t, &written)
	sess.SetValue(coregateway.SessionValueUID, "private-sender")
	traceOrdinal := 0
	handler := New(Options{Messages: message.New(message.Options{Submitter: preparationEntrySubmitter{}}), OwnerNodeID: 1, TraceIDGenerator: func() string {
		traceOrdinal++
		return fmt.Sprintf("%032x", traceOrdinal)
	}})
	items := make([]coregateway.SendBatchItem, 2)
	keys := make([]sendtrace.AttemptKey, len(items))
	for i := range items {
		keys[i] = sendtrace.AttemptKey{MessageFingerprint: sendtrace.MessageFingerprint("same-logical"), ClientSeq: uint32(i + 1), ConnectFingerprint: [16]byte{9}}
		token := store.BeginSendAttempt(keys[i], time.Now())
		items[i] = coregateway.SendBatchItem{Context: coregateway.Context{Session: sess, RequestContext: sendtrace.WithGatewayAttempt(context.Background(), token)}, Frame: &frame.SendPacket{ClientSeq: uint64(i + 1), ClientMsgNo: "same-logical", ChannelID: "private-group", ChannelType: 2, Payload: []byte("body")}}
	}
	if err := handler.OnSendBatch(items); err != nil {
		t.Fatal(err)
	}
	if len(written) != len(items) {
		t.Fatal("observation changed original SENDACK cardinality")
	}
	query := store.QuerySendAttempts(context.Background(), keys)
	for i, item := range query.Results {
		if item.Status != "candidate" || len(item.Candidates) != 1 || requireSendack(t, written, i).ClientSeq != uint64(i+1) {
			t.Fatal("original invocation or SENDACK order changed")
		}
		candidate := item.Candidates[0]
		found := 0
		for _, phase := range candidate.Phases {
			if phase.Stage == "message.permission_batch" || phase.Stage == "message.pre_append" {
				if !phase.Observed || phase.StartedAt.IsZero() || !phase.EndDerived {
					t.Fatalf("actual entry lost preparation: %+v", phase)
				}
				found++
			}
		}
		if found != 2 {
			t.Fatalf("actual Gateway→Message→summary path retained %d preparation facts", found)
		}
	}
}
