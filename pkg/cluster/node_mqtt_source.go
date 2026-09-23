package cluster

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// EnsureChannelMQTTSource routes source protection through the hosted Channel
// service. Foreground admission and the exact caller fences remain mandatory.
func (n *Node) EnsureChannelMQTTSource(ctx context.Context, req ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if n.channels == nil {
		return ch.MQTTSourceSnapshot{}, ErrNotStarted
	}
	activator, ok := n.channels.(ch.MQTTSourceActivator)
	if !ok {
		return ch.MQTTSourceSnapshot{}, ch.ErrInvalidConfig
	}
	return activator.EnsureMQTTSource(ctx, req)
}

// GetChannelRuntimeMetaFresh returns one bounded, freshly confirmed Slot read.
func (n *Node) GetChannelRuntimeMetaFresh(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.ChannelRuntimeMeta{}, err
	}
	if n.defaultSlotProxy == nil {
		return metadb.ChannelRuntimeMeta{}, ErrNotStarted
	}
	return n.defaultSlotProxy.GetChannelRuntimeMetaFresh(ctx, id, typ)
}

func (s defaultChannelRuntimeMetaStore) GetChannelRuntimeMetaFresh(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	if s.node == nil {
		return metadb.ChannelRuntimeMeta{}, ErrNotStarted
	}
	return s.node.GetChannelRuntimeMetaFresh(ctx, id, typ)
}
