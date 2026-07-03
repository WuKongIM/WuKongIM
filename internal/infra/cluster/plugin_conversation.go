package cluster

import (
	"context"
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// PluginConversationNode exposes UID-owned active conversation rows for plugin host RPCs.
type PluginConversationNode interface {
	// ListConversationActivePage reads one page of UID-owned active conversation rows.
	ListConversationActivePage(context.Context, metadb.ConversationKind, string, metadb.ConversationActiveCursor, int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
}

// PluginConversationReader adapts active conversation rows to plugin channel IDs.
type PluginConversationReader struct {
	node PluginConversationNode
}

// NewPluginConversationReader creates a PluginConversationReader.
func NewPluginConversationReader(node PluginConversationNode) *PluginConversationReader {
	return &PluginConversationReader{node: node}
}

// ConversationChannels reads recent normal conversation channel IDs for one UID.
func (r *PluginConversationReader) ConversationChannels(ctx context.Context, uid string, limit int) ([]message.ChannelID, error) {
	if r == nil || r.node == nil {
		return nil, pluginusecase.ErrConversationReaderRequired
	}
	if limit <= 0 {
		return []message.ChannelID{}, nil
	}
	rows, _, _, err := r.node.ListConversationActivePage(ctx, metadb.ConversationKindNormal, uid, metadb.ConversationActiveCursor{}, limit)
	if err != nil {
		return nil, err
	}
	out := make([]message.ChannelID, 0, len(rows))
	for _, row := range rows {
		if row.ChannelType < 0 || row.ChannelType > math.MaxUint8 {
			return nil, fmt.Errorf("invalid conversation channel type: %d", row.ChannelType)
		}
		out = append(out, message.ChannelID{ID: row.ChannelID, Type: uint8(row.ChannelType)})
	}
	return out, nil
}
