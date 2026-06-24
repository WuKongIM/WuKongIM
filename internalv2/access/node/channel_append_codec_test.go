package node

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
)

func TestChannelAppendCodecRejectsEmptyRequest(t *testing.T) {
	if _, err := decodeChannelAppendRequest(nil); err == nil {
		t.Fatal("decodeChannelAppendRequest(nil) error = nil, want malformed payload error")
	}

	body, err := encodeChannelAppendRequest(channelAppendRequest{Target: channelAppendTestTarget()})
	if err != nil {
		t.Fatalf("encodeChannelAppendRequest() error = %v", err)
	}
	if _, err := decodeChannelAppendRequest(body); err == nil {
		t.Fatal("decodeChannelAppendRequest(empty items) error = nil, want empty request error")
	}
}

func TestChannelAppendCodecRoundTripsOneItem(t *testing.T) {
	req := channelAppendRequest{
		Target: channelAppendTestTarget(),
		Items: []channelAppendItem{{
			Command: channelAppendTestCommand(),
			Timeout: 250 * time.Millisecond,
		}},
	}

	body, err := encodeChannelAppendRequest(req)
	if err != nil {
		t.Fatalf("encodeChannelAppendRequest() error = %v", err)
	}
	got, err := decodeChannelAppendRequest(body)
	if err != nil {
		t.Fatalf("decodeChannelAppendRequest() error = %v", err)
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

func TestChannelAppendCodecClonesPayloadAndScopedUIDs(t *testing.T) {
	cmd := channelAppendTestCommand()
	body, err := encodeChannelAppendRequest(channelAppendRequest{
		Target: channelAppendTestTarget(),
		Items:  []channelAppendItem{{Command: cmd}},
	})
	if err != nil {
		t.Fatalf("encodeChannelAppendRequest() error = %v", err)
	}

	got, err := decodeChannelAppendRequest(body)
	if err != nil {
		t.Fatalf("decodeChannelAppendRequest() error = %v", err)
	}
	got.Items[0].Command.Payload[0] = 'X'
	got.Items[0].Command.MessageScopedUIDs[0] = "changed"

	again, err := decodeChannelAppendRequest(body)
	if err != nil {
		t.Fatalf("decodeChannelAppendRequest(second) error = %v", err)
	}
	if string(again.Items[0].Command.Payload) != "hello" {
		t.Fatalf("payload after mutation = %q, want hello", string(again.Items[0].Command.Payload))
	}
	if again.Items[0].Command.MessageScopedUIDs[0] != "u2" {
		t.Fatalf("scoped uid after mutation = %q, want u2", again.Items[0].Command.MessageScopedUIDs[0])
	}
}

func TestChannelAppendCodecRejectsOversizedItems(t *testing.T) {
	body := make([]byte, 0, 64+maxChannelAppendCollectionLen+1)
	body = append(body, channelAppendRequestMagic[:]...)
	body = appendChannelAppendTarget(body, channelAppendTestTarget())
	body = appendUvarint(body, uint64(maxChannelAppendCollectionLen+1))
	body = append(body, make([]byte, maxChannelAppendCollectionLen+1)...)

	if _, err := decodeChannelAppendRequest(body); err == nil {
		t.Fatal("decodeChannelAppendRequest() error = nil, want oversized collection error")
	}
}

func TestChannelAppendCodecRejectsTrailingBytes(t *testing.T) {
	body, err := encodeChannelAppendRequest(channelAppendRequest{
		Target: channelAppendTestTarget(),
		Items:  []channelAppendItem{{Command: channelAppendTestCommand()}},
	})
	if err != nil {
		t.Fatalf("encodeChannelAppendRequest() error = %v", err)
	}
	body = append(body, 0)
	if _, err := decodeChannelAppendRequest(body); err == nil {
		t.Fatal("decodeChannelAppendRequest() error = nil, want trailing bytes error")
	}
}

func TestChannelAppendCodecResultErrorsPreserveStableStatuses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "ok"},
		{name: "not leader", err: channelappend.ErrNotLeader, want: channelappend.ErrNotLeader},
		{name: "not channel authority", err: channelappend.ErrNotChannelAuthority, want: channelappend.ErrNotChannelAuthority},
		{name: "stale route", err: channelappend.ErrStaleRoute, want: channelappend.ErrStaleRoute},
		{name: "route not ready", err: channelappend.ErrRouteNotReady, want: channelappend.ErrRouteNotReady},
		{name: "backpressured", err: channelappend.ErrBackpressured, want: channelappend.ErrBackpressured},
		{name: "append result missing", err: channelappend.ErrAppendResultMissing, want: channelappend.ErrAppendResultMissing},
		{name: "channel busy", err: channelappend.ErrChannelBusy, want: channelappend.ErrChannelBusy},
		{name: "context canceled", err: context.Canceled, want: context.Canceled},
		{name: "context deadline exceeded", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "rejected", err: errors.New("boom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := channelappend.SendBatchItemResult{
				Result: channelappend.SendResult{MessageID: 1001, MessageSeq: 9, Reason: channelappend.ReasonSuccess},
				Err:    tt.err,
			}
			body, err := encodeChannelAppendResponse(channelAppendResponse{Status: rpcStatusOK, Results: []channelappend.SendBatchItemResult{result}})
			if err != nil {
				t.Fatalf("encodeChannelAppendResponse() error = %v", err)
			}
			got, err := decodeChannelAppendResponse(body)
			if err != nil {
				t.Fatalf("decodeChannelAppendResponse() error = %v", err)
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

func channelAppendTestTarget() channelappend.AuthorityTarget {
	return channelappend.AuthorityTarget{
		ChannelID:                 channelappend.ChannelID{ID: "g1", Type: 2},
		ChannelKey:                "channel-key",
		LeaderNodeID:              3,
		Epoch:                     4,
		LeaderEpoch:               5,
		Large:                     true,
		SubscriberMutationVersion: 7,
	}
}

func channelAppendTestCommand() channelappend.SendCommand {
	return channelappend.SendCommand{
		FromUID:                "u1",
		DeviceID:               "d1",
		DeviceFlag:             3,
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
		Setting:                7,
		Topic:                  "topic-1",
		Expire:                 3600,
		ProtocolVersion:        4,
		Origin:                 channelappend.SendOriginPlugin,
		HookDepth:              1,
		SkipPluginHooks:        true,
	}
}
