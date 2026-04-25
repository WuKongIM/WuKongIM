package app

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
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
	if meta.Leader == r.localNodeID {
		return r.queryLocal(req)
	}
	if r.remote == nil {
		return managementusecase.MessageQueryPage{}, channel.ErrStaleMeta
	}
	return r.queryRemote(ctx, meta.Leader, req)
}

func (r managerMessageReader) queryLocal(req managementusecase.MessageQueryRequest) (managementusecase.MessageQueryPage, error) {
	committedHW, err := channelhandler.LoadCommittedHW(r.channelLog, req.ChannelID)
	if err != nil {
		return managementusecase.MessageQueryPage{}, err
	}
	page, err := channelhandler.QueryMessages(r.channelLog, committedHW, channelhandler.QueryMessagesRequest{
		ChannelID:   req.ChannelID,
		BeforeSeq:   req.BeforeSeq,
		Limit:       req.Limit,
		MessageID:   req.MessageID,
		ClientMsgNo: req.ClientMsgNo,
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

func (r managerMessageReader) queryRemote(ctx context.Context, nodeID uint64, req managementusecase.MessageQueryRequest) (managementusecase.MessageQueryPage, error) {
	page, err := r.remote.QueryChannelMessages(ctx, nodeID, accessnode.ChannelMessagesQuery{
		ChannelID:   req.ChannelID,
		BeforeSeq:   req.BeforeSeq,
		Limit:       req.Limit,
		MessageID:   req.MessageID,
		ClientMsgNo: req.ClientMsgNo,
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
