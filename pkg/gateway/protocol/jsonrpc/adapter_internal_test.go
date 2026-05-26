package jsonrpc

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	pkgjsonrpc "github.com/WuKongIM/WuKongIM/pkg/protocol/jsonrpc"
)

func TestAdapterStoresReplyTokensOnSession(t *testing.T) {
	adapter := New()
	sess := session.New(session.Config{
		ID:         1,
		Listener:   "fake-listener",
		RemoteAddr: "fake-remote",
		LocalAddr:  "fake-local",
	})
	payload, err := pkgjsonrpc.Encode(pkgjsonrpc.PingRequest{
		BaseRequest: pkgjsonrpc.BaseRequest{
			Jsonrpc: "2.0",
			Method:  pkgjsonrpc.MethodPing,
			ID:      "req-session",
		},
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if _, _, err := adapter.Decode(sess, payload); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if _, ok := sess.Value(replyTokenQueueSessionValue).(*replyTokenQueue); !ok {
		t.Fatalf("reply token queue was not stored on session")
	}
}
