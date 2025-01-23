package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type channelTransport struct {
	s *Server
	wklog.Log
}

func newChannelTransport(s *Server) *channelTransport {
	return &channelTransport{
		s:   s,
		Log: wklog.NewWKLog("channelTransport"),
	}
}

func (c *channelTransport) Send(key string, event types.Event) {
	to := event.To
	if to == 0 {
		c.Error("Send event to node id is 0", zap.String("key", key), zap.Uint64("to", to), zap.String("event", event.String()))
		return
	}

	node := c.s.nodeManager.node(to)
	if node == nil {
		c.Error("Send event to node is nil", zap.String("key", key), zap.Uint64("to", to), zap.String("event", event.String()))
		return
	}

	data, err := event.Marshal()
	if err != nil {
		c.Error("Send event marshal failed", zap.Error(err), zap.String("key", key))
		return
	}
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(key)
	enc.WriteBytes(data)

	msg := &proto.Message{
		MsgType: MsgTypeChannel,
		Content: enc.Bytes(),
	}

	if trace.GlobalTrace != nil {
		if event.Type == types.SyncReq {
			trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingCountAdd(trace.ClusterKindChannel, 1)
			trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingBytesAdd(trace.ClusterKindChannel, int64(msg.Size()))
		}
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingCountAdd(trace.ClusterKindChannel, 1)
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingBytesAdd(trace.ClusterKindChannel, int64(msg.Size()))
	}

	err = node.send(msg)
	if err != nil {
		c.Error("Send event failed", zap.Error(err), zap.String("key", key), zap.String("event", event.String()))
		return
	}
}
