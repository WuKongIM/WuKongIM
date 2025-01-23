package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

type nodeTransport struct {
	s *Server
	wklog.Log
}

func newNodeTransport(s *Server) *nodeTransport {
	return &nodeTransport{
		s:   s,
		Log: wklog.NewWKLog("nodeTransport"),
	}
}

func (n *nodeTransport) Send(event types.Event) {
	to := event.To
	if to == 0 {
		n.Error("Send event to node id is 0", zap.Uint64("to", to), zap.String("event", event.String()))
		return
	}

	node := n.s.nodeManager.node(to)
	if node == nil {
		n.Error("Send event to node is nil", zap.Uint64("to", to), zap.String("event", event.String()))
		return
	}

	data, err := event.Marshal()
	if err != nil {
		n.Error("Send event marshal failed", zap.Error(err))
		return
	}

	msg := &proto.Message{
		MsgType: MsgTypeNode,
		Content: data,
	}
	if trace.GlobalTrace != nil {
		if event.Type == types.SyncReq {
			trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingCountAdd(trace.ClusterKindConfig, 1)
			trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingBytesAdd(trace.ClusterKindConfig, int64(msg.Size()))
		}
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingCountAdd(trace.ClusterKindConfig, 1)
		trace.GlobalTrace.Metrics.Cluster().MessageOutgoingBytesAdd(trace.ClusterKindConfig, int64(msg.Size()))
	}

	err = node.send(msg)
	if err != nil {
		n.Error("Send event failed", zap.Error(err), zap.String("event", event.String()))
	}
}
