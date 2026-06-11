package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
)

func TestChannelWriteCodecRejectsEmptyRequest(t *testing.T) {
	if _, err := decodeChannelWriteRequest(nil); err == nil {
		t.Fatal("decodeChannelWriteRequest(nil) error = nil, want malformed payload error")
	}

	body, err := encodeChannelWriteRequest(channelWriteRequest{Target: channelWriteTestTarget()})
	if err != nil {
		t.Fatalf("encodeChannelWriteRequest() error = %v", err)
	}
	if _, err := decodeChannelWriteRequest(body); err == nil {
		t.Fatal("decodeChannelWriteRequest(empty items) error = nil, want empty request error")
	}
}

func TestChannelWriteCodecRoundTripsOneItem(t *testing.T) {
	req := channelWriteRequest{
		Target: channelWriteTestTarget(),
		Items: []channelWriteItem{{
			Command: channelWriteTestCommand(),
			Timeout: 250 * time.Millisecond,
		}},
	}

	body, err := encodeChannelWriteRequest(req)
	if err != nil {
		t.Fatalf("encodeChannelWriteRequest() error = %v", err)
	}
	got, err := decodeChannelWriteRequest(body)
	if err != nil {
		t.Fatalf("decodeChannelWriteRequest() error = %v", err)
	}

	if !reflect.DeepEqual(got.Target, req.Target) {
		t.Fatalf("target = %#v, want %#v", got.Target, req.Target)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(got.Items))
	}
	if got.Items[0].Timeout != req.Items[0].Timeout {
		t.Fatalf("timeout = %s, want %s", got.Items[0].Timeout, req.Items[0].Timeout)
	}
	if !reflect.DeepEqual(got.Items[0].Command, req.Items[0].Command) {
		t.Fatalf("command = %#v, want %#v", got.Items[0].Command, req.Items[0].Command)
	}
}

func TestChannelWriteCodecClonesPayloadAndScopedUIDs(t *testing.T) {
	cmd := channelWriteTestCommand()
	body, err := encodeChannelWriteRequest(channelWriteRequest{
		Target: channelWriteTestTarget(),
		Items:  []channelWriteItem{{Command: cmd}},
	})
	if err != nil {
		t.Fatalf("encodeChannelWriteRequest() error = %v", err)
	}

	got, err := decodeChannelWriteRequest(body)
	if err != nil {
		t.Fatalf("decodeChannelWriteRequest() error = %v", err)
	}
	got.Items[0].Command.Payload[0] = 'X'
	got.Items[0].Command.MessageScopedUIDs[0] = "changed"

	again, err := decodeChannelWriteRequest(body)
	if err != nil {
		t.Fatalf("decodeChannelWriteRequest(second) error = %v", err)
	}
	if string(again.Items[0].Command.Payload) != "hello" {
		t.Fatalf("payload after mutation = %q, want hello", string(again.Items[0].Command.Payload))
	}
	if again.Items[0].Command.MessageScopedUIDs[0] != "u2" {
		t.Fatalf("scoped uid after mutation = %q, want u2", again.Items[0].Command.MessageScopedUIDs[0])
	}
}

func TestChannelWriteCodecRejectsOversizedItems(t *testing.T) {
	body := make([]byte, 0, 64+maxChannelWriteCollectionLen+1)
	body = append(body, channelWriteRequestMagic[:]...)
	body = appendChannelWriteTarget(body, channelWriteTestTarget())
	body = appendUvarint(body, uint64(maxChannelWriteCollectionLen+1))
	body = append(body, make([]byte, maxChannelWriteCollectionLen+1)...)

	if _, err := decodeChannelWriteRequest(body); err == nil {
		t.Fatal("decodeChannelWriteRequest() error = nil, want oversized collection error")
	}
}

func TestChannelWriteCodecRejectsTrailingBytes(t *testing.T) {
	body, err := encodeChannelWriteRequest(channelWriteRequest{
		Target: channelWriteTestTarget(),
		Items:  []channelWriteItem{{Command: channelWriteTestCommand()}},
	})
	if err != nil {
		t.Fatalf("encodeChannelWriteRequest() error = %v", err)
	}
	body = append(body, 0)
	if _, err := decodeChannelWriteRequest(body); err == nil {
		t.Fatal("decodeChannelWriteRequest() error = nil, want trailing bytes error")
	}
}

func TestChannelWriteCodecResultErrorsPreserveStableStatuses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "ok"},
		{name: "not leader", err: channelwrite.ErrNotLeader, want: channelwrite.ErrNotLeader},
		{name: "not channel authority", err: channelwrite.ErrNotChannelAuthority, want: channelwrite.ErrNotChannelAuthority},
		{name: "stale route", err: channelwrite.ErrStaleRoute, want: channelwrite.ErrStaleRoute},
		{name: "route not ready", err: channelwrite.ErrRouteNotReady, want: channelwrite.ErrRouteNotReady},
		{name: "backpressured", err: channelwrite.ErrBackpressured, want: channelwrite.ErrBackpressured},
		{name: "append result missing", err: channelwrite.ErrAppendResultMissing, want: channelwrite.ErrAppendResultMissing},
		{name: "channel busy", err: channelwrite.ErrChannelBusy, want: channelwrite.ErrChannelBusy},
		{name: "context canceled", err: context.Canceled, want: context.Canceled},
		{name: "context deadline exceeded", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "rejected", err: errors.New("boom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := channelwrite.SendBatchItemResult{
				Result: channelwrite.SendResult{MessageID: 1001, MessageSeq: 9, Reason: channelwrite.ReasonSuccess},
				Err:    tt.err,
			}
			body, err := encodeChannelWriteResponse(channelWriteResponse{Status: rpcStatusOK, Results: []channelwrite.SendBatchItemResult{result}})
			if err != nil {
				t.Fatalf("encodeChannelWriteResponse() error = %v", err)
			}
			got, err := decodeChannelWriteResponse(body)
			if err != nil {
				t.Fatalf("decodeChannelWriteResponse() error = %v", err)
			}
			if len(got.Results) != 1 {
				t.Fatalf("results len = %d, want 1", len(got.Results))
			}
			if got.Results[0].Result != result.Result {
				t.Fatalf("result = %#v, want %#v", got.Results[0].Result, result.Result)
			}
			switch {
			case tt.want != nil:
				if !errors.Is(got.Results[0].Err, tt.want) {
					t.Fatalf("Err = %v, want %v", got.Results[0].Err, tt.want)
				}
			case tt.err == nil:
				if got.Results[0].Err != nil {
					t.Fatalf("Err = %v, want nil", got.Results[0].Err)
				}
			default:
				if got.Results[0].Err == nil || got.Results[0].Err.Error() != "internalv2/access/node: boom" {
					t.Fatalf("Err = %v, want rejected message", got.Results[0].Err)
				}
			}
		})
	}
}

func channelWriteTestTarget() channelwrite.AuthorityTarget {
	return channelwrite.AuthorityTarget{
		ChannelID:                 channelwrite.ChannelID{ID: "g1", Type: 2},
		ChannelKey:                "channel-key",
		LeaderNodeID:              3,
		Epoch:                     4,
		LeaderEpoch:               5,
		RouteRevision:             6,
		Large:                     true,
		SubscriberMutationVersion: 7,
	}
}

func channelWriteTestCommand() channelwrite.SendCommand {
	return channelwrite.SendCommand{
		FromUID:                "u1",
		SenderNodeID:           11,
		SenderSessionID:        12,
		ClientSeq:              13,
		ClientMsgNo:            "client-1",
		TraceID:                "trace-1",
		ChannelKey:             "channel-key",
		ChannelID:              "g1",
		ChannelType:            2,
		Payload:                []byte("hello"),
		NoPersist:              true,
		SyncOnce:               true,
		RedDot:                 true,
		NormalizePersonChannel: true,
		RequestScoped:          true,
		MessageScopedUIDs:      []string{"u2", "u3"},
		MessageID:              99,
		ProtocolVersion:        4,
	}
}
