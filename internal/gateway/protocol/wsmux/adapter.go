package wsmux

import (
	"bytes"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/gateway/protocol"
	protojsonrpc "github.com/WuKongIM/WuKongIM/internal/gateway/protocol/jsonrpc"
	protowkproto "github.com/WuKongIM/WuKongIM/internal/gateway/protocol/wkproto"
	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const Name = "wsmux"

type Adapter struct {
	wkproto *protowkproto.Adapter
	jsonrpc *protojsonrpc.Adapter
}

var _ protocol.ReplyTokenTracker = (*Adapter)(nil)

func New() *Adapter {
	return &Adapter{
		wkproto: protowkproto.New(),
		jsonrpc: protojsonrpc.New(),
	}
}

func (a *Adapter) Name() string {
	if a == nil {
		return ""
	}
	return Name
}

func (a *Adapter) Decode(sess session.Session, in []byte) ([]frame.Frame, int, error) {
	adapter, protocolName, err := a.resolveAdapter(sess, in)
	if err != nil || adapter == nil {
		return nil, 0, err
	}
	if sess != nil && protocolName != "" {
		sess.SetValue(gatewaytypes.SessionValueProtocolName, protocolName)
	}
	return adapter.Decode(sess, in)
}

func (a *Adapter) Encode(sess session.Session, f frame.Frame, meta session.OutboundMeta) ([]byte, error) {
	adapter, _, err := a.resolveAdapter(sess, nil)
	if err != nil {
		return nil, err
	}
	if adapter == nil {
		return nil, fmt.Errorf("gateway/protocol/wsmux: protocol not selected")
	}
	return adapter.Encode(sess, f, meta)
}

func (a *Adapter) OnOpen(session.Session) error {
	return nil
}

func (a *Adapter) OnClose(sess session.Session) error {
	adapter, _, err := a.resolveAdapter(sess, nil)
	if err != nil || adapter == nil {
		return nil
	}
	return adapter.OnClose(sess)
}

func (a *Adapter) TakeReplyTokens(sess session.Session, count int) []string {
	if count <= 0 {
		return nil
	}
	adapter, protocolName, err := a.resolveAdapter(sess, nil)
	if err != nil || protocolName != protojsonrpc.Name {
		return nil
	}
	tracker, ok := adapter.(protocol.ReplyTokenTracker)
	if !ok {
		return nil
	}
	return tracker.TakeReplyTokens(sess, count)
}

func (a *Adapter) resolveAdapter(sess session.Session, in []byte) (protocol.Adapter, string, error) {
	if a == nil {
		return nil, "", fmt.Errorf("gateway/protocol/wsmux: nil adapter")
	}

	if protocolName := selectedProtocol(sess); protocolName != "" && protocolName != Name {
		return a.adapterForProtocol(protocolName)
	}

	trimmed := bytes.TrimLeft(in, " \t\r\n")
	if len(trimmed) == 0 {
		return nil, "", nil
	}
	switch trimmed[0] {
	case '{', '[':
		return a.jsonrpc, protojsonrpc.Name, nil
	default:
		return a.wkproto, protowkproto.Name, nil
	}
}

func (a *Adapter) adapterForProtocol(protocolName string) (protocol.Adapter, string, error) {
	switch protocolName {
	case protojsonrpc.Name:
		return a.jsonrpc, protocolName, nil
	case protowkproto.Name:
		return a.wkproto, protocolName, nil
	default:
		return nil, "", fmt.Errorf("gateway/protocol/wsmux: unsupported protocol %q", protocolName)
	}
}

func selectedProtocol(sess session.Session) string {
	if sess == nil {
		return ""
	}
	value, _ := sess.Value(gatewaytypes.SessionValueProtocolName).(string)
	return value
}
