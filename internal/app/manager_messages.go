package app

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type managerMessageMetas interface {
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
}

type managerMessageRemote interface {
	QueryChannelMessages(ctx context.Context, nodeID uint64, req accessnode.ChannelMessagesQuery) (accessnode.ChannelMessagesPage, error)
}

type managerMessageReader struct {
	localNodeID uint64
	channelLog  *channelstore.Engine
	metas       managerMessageMetas
	remote      managerMessageRemote
}

func (r managerMessageReader) QueryMessages(ctx context.Context, req managementusecase.MessageQueryRequest) (managementusecase.MessageQueryPage, error) {
	if r.channelLog == nil || r.metas == nil {
		return managementusecase.MessageQueryPage{}, nil
	}

	meta, err := r.metas.GetChannelRuntimeMeta(ctx, req.ChannelID.ID, int64(req.ChannelID.Type))
	if err != nil {
		return managementusecase.MessageQueryPage{}, err
	}
	if meta.Leader == 0 {
		return managementusecase.MessageQueryPage{}, raftcluster.ErrNoLeader
	}
	minAvailableSeq := managerMessageMinAvailableSeq(meta)
	if meta.Leader == r.localNodeID {
		return r.queryLocal(req, minAvailableSeq)
	}
	if r.remote == nil {
		return managementusecase.MessageQueryPage{}, channel.ErrStaleMeta
	}
	return r.queryRemote(ctx, meta.Leader, req, minAvailableSeq)
}

func (r managerMessageReader) MaxMessageSeq(ctx context.Context, id channel.ChannelID) (uint64, error) {
	if r.channelLog == nil || r.metas == nil {
		return 0, nil
	}

	meta, err := r.metas.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		return 0, err
	}
	return r.MaxMessageSeqForMeta(ctx, meta)
}

func (r managerMessageReader) MaxMessageSeqForMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (uint64, error) {
	if r.channelLog == nil {
		return 0, nil
	}
	if meta.Leader == 0 {
		return 0, raftcluster.ErrNoLeader
	}
	id := channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)}
	if meta.Leader == r.localNodeID {
		return channelhandler.LoadCommittedHW(r.channelLog, id)
	}
	if r.remote == nil {
		return 0, channel.ErrStaleMeta
	}
	page, err := r.remote.QueryChannelMessages(ctx, meta.Leader, accessnode.ChannelMessagesQuery{
		ChannelID:  id,
		MaxSeqOnly: true,
	})
	if err != nil {
		return 0, err
	}
	return page.MaxMessageSeq, nil
}

func (r managerMessageReader) queryLocal(req managementusecase.MessageQueryRequest, minAvailableSeq uint64) (managementusecase.MessageQueryPage, error) {
	committedHW, err := channelhandler.LoadCommittedHW(r.channelLog, req.ChannelID)
	if err != nil {
		return managementusecase.MessageQueryPage{}, err
	}
	page, err := channelhandler.QueryMessages(r.channelLog, committedHW, channelhandler.QueryMessagesRequest{
		ChannelID:       req.ChannelID,
		BeforeSeq:       req.BeforeSeq,
		Limit:           req.Limit,
		MessageID:       req.MessageID,
		ClientMsgNo:     req.ClientMsgNo,
		MinAvailableSeq: minAvailableSeq,
	})
	if err != nil {
		return managementusecase.MessageQueryPage{}, err
	}
	return managementusecase.MessageQueryPage{
		Items:         append([]channel.Message(nil), page.Messages...),
		HasMore:       page.HasMore,
		NextBeforeSeq: page.NextBeforeSeq,
	}, nil
}

func (r managerMessageReader) SyncMessages(ctx context.Context, req messageusecase.ChannelMessageQuery) (messageusecase.ChannelMessagePage, error) {
	if r.channelLog == nil || r.metas == nil {
		return messageusecase.ChannelMessagePage{}, nil
	}

	meta, err := r.metas.GetChannelRuntimeMeta(ctx, req.ChannelID.ID, int64(req.ChannelID.Type))
	if err != nil {
		return messageusecase.ChannelMessagePage{}, err
	}
	if meta.Leader == 0 {
		return messageusecase.ChannelMessagePage{}, raftcluster.ErrNoLeader
	}
	minAvailableSeq := managerMessageMinAvailableSeq(meta)
	if meta.Leader == r.localNodeID {
		return r.syncLocal(req, minAvailableSeq)
	}
	if r.remote == nil {
		return messageusecase.ChannelMessagePage{}, channel.ErrStaleMeta
	}
	return r.syncRemote(ctx, meta.Leader, req, minAvailableSeq)
}

func (r managerMessageReader) syncLocal(req messageusecase.ChannelMessageQuery, minAvailableSeq uint64) (messageusecase.ChannelMessagePage, error) {
	committedHW, err := channelhandler.LoadCommittedHW(r.channelLog, req.ChannelID)
	if err != nil {
		return messageusecase.ChannelMessagePage{}, err
	}
	page, err := channelhandler.SyncMessages(r.channelLog, committedHW, channelhandler.SyncMessagesRequest{
		ChannelID:       req.ChannelID,
		StartSeq:        req.StartSeq,
		EndSeq:          req.EndSeq,
		Limit:           req.Limit,
		PullMode:        channelhandler.SyncPullMode(req.PullMode),
		MinAvailableSeq: minAvailableSeq,
	})
	if err != nil {
		return messageusecase.ChannelMessagePage{}, err
	}
	return messageusecase.ChannelMessagePage{
		Messages: append([]channel.Message(nil), page.Messages...),
		HasMore:  page.HasMore,
	}, nil
}

func (r managerMessageReader) syncRemote(ctx context.Context, nodeID uint64, req messageusecase.ChannelMessageQuery, minAvailableSeq uint64) (messageusecase.ChannelMessagePage, error) {
	page, err := r.remote.QueryChannelMessages(ctx, nodeID, accessnode.ChannelMessagesQuery{
		ChannelID:       req.ChannelID,
		SyncMode:        true,
		StartSeq:        req.StartSeq,
		EndSeq:          req.EndSeq,
		Limit:           req.Limit,
		PullMode:        uint8(req.PullMode),
		MinAvailableSeq: minAvailableSeq,
	})
	if err != nil {
		return messageusecase.ChannelMessagePage{}, err
	}
	return messageusecase.ChannelMessagePage{
		Messages: append([]channel.Message(nil), page.Messages...),
		HasMore:  page.HasMore,
	}, nil
}

func (r managerMessageReader) queryRemote(ctx context.Context, nodeID uint64, req managementusecase.MessageQueryRequest, minAvailableSeq uint64) (managementusecase.MessageQueryPage, error) {
	page, err := r.remote.QueryChannelMessages(ctx, nodeID, accessnode.ChannelMessagesQuery{
		ChannelID:       req.ChannelID,
		BeforeSeq:       req.BeforeSeq,
		Limit:           req.Limit,
		MessageID:       req.MessageID,
		ClientMsgNo:     req.ClientMsgNo,
		MinAvailableSeq: minAvailableSeq,
	})
	if err != nil {
		return managementusecase.MessageQueryPage{}, err
	}
	return managementusecase.MessageQueryPage{
		Items:         append([]channel.Message(nil), page.Messages...),
		HasMore:       page.HasMore,
		NextBeforeSeq: page.NextBeforeSeq,
	}, nil
}

func managerMessageMinAvailableSeq(meta metadb.ChannelRuntimeMeta) uint64 {
	return channel.EffectiveMinAvailableSeq(meta.RetentionThroughSeq, 0)
}
