package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

type sendBanMetadataNode interface {
	SetSendBanMetadata(context.Context, metadb.SendBanMutation) (metadb.SendBanResult, error)
	ReadSendPermissionMetadataBatch(context.Context, []proxy.PermissionMetadataRead) []proxy.PermissionMetadataReadResult
}

func setSendBan(ctx context.Context, node any, q metadb.SendBanMutation) (metadb.SendBanResult, error) {
	n, ok := node.(sendBanMetadataNode)
	if !ok {
		return metadb.SendBanResult{}, clusterpkg.ErrRouteNotReady
	}
	return n.SetSendBanMetadata(ctx, q)
}
func getSendBan(ctx context.Context, node any, q proxy.PermissionMetadataRead) (metadb.SendBanResult, error) {
	n, ok := node.(sendBanMetadataNode)
	if !ok {
		return metadb.SendBanResult{}, clusterpkg.ErrRouteNotReady
	}
	rows := n.ReadSendPermissionMetadataBatch(ctx, []proxy.PermissionMetadataRead{q})
	if len(rows) != 1 {
		return metadb.SendBanResult{}, metadb.ErrCorruptValue
	}
	r := rows[0]
	if r.Err != nil {
		return metadb.SendBanResult{}, mapChannelPermissionReadError(r.Err)
	}
	if q.Kind == proxy.PermissionMetadataReadUserSendPolicy {
		return r.UserPolicy, nil
	}
	if !r.Found && q.ChannelType != 1 {
		return metadb.SendBanResult{Status: "not_found"}, nil
	}
	return metadb.SendBanResult{Status: "ok", SendBan: r.Channel.SendBan, Version: r.Channel.SendBanVersion}, nil
}

// SetUserSendBan persists a UID-owned atomic restriction.
func (s *UserMetadataStore) SetUserSendBan(ctx context.Context, q metadb.SendBanMutation) (metadb.SendBanResult, error) {
	return setSendBan(ctx, s.node, q)
}

// GetUserSendBan reads the current policy without returning credential metadata.
func (s *UserMetadataStore) GetUserSendBan(ctx context.Context, uid string) (metadb.SendBanResult, error) {
	return getSendBan(ctx, s.node, proxy.PermissionMetadataRead{Kind: proxy.PermissionMetadataReadUserSendPolicy, UID: uid})
}

// SetChannelSendBan updates the actual source Channel's sending restriction.
func (s *ChannelMetadataStore) SetChannelSendBan(ctx context.Context, q metadb.SendBanMutation) (metadb.SendBanResult, error) {
	return setSendBan(ctx, s.node, q)
}

// GetChannelSendBan reads one source Channel policy from fresh Slot authority.
func (s *ChannelMetadataStore) GetChannelSendBan(ctx context.Context, id string, kind int64) (metadb.SendBanResult, error) {
	return getSendBan(ctx, s.node, proxy.PermissionMetadataRead{Kind: proxy.PermissionMetadataReadChannel, ChannelID: id, ChannelType: kind})
}

// GetUserSendPolicy supplies the scalar embedding path with fresh UID policy.
func (s *ChannelMetadataStore) GetUserSendPolicy(ctx context.Context, uid string) (metadb.SendBanResult, error) {
	return getSendBan(ctx, s.node, proxy.PermissionMetadataRead{Kind: proxy.PermissionMetadataReadUserSendPolicy, UID: uid})
}

// UpdateChannelInfo keeps optional policy intent in the same atomic proposal.
func (s *ChannelMetadataStore) UpdateChannelInfo(ctx context.Context, q metadb.ChannelInfoMutation) (metadb.SendBanResult, error) {
	n, ok := s.node.(interface {
		UpdateChannelInfoMetadata(context.Context, metadb.ChannelInfoMutation) (metadb.SendBanResult, error)
	})
	if !ok {
		return metadb.SendBanResult{}, clusterpkg.ErrRouteNotReady
	}
	// A proposal timeout may still have committed; discard the pre-write view.
	defer s.appendMetadataCache.Delete(channelappend.ChannelID{ID: q.ChannelID, Type: uint8(q.ChannelType)})
	return n.UpdateChannelInfoMetadata(ctx, q)
}
