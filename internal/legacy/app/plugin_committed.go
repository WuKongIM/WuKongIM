package app

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/legacy/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	errPluginCommittedNodeClientRequired = errors.New("app: plugin committed node client required")
)

type pluginPersistAfterUsecase interface {
	PersistAfterCommitted(context.Context, messageevents.MessageCommitted) error
}

type pluginCommittedSubmitter interface {
	SubmitPluginCommitted(ctx context.Context, nodeID uint64, event messageevents.MessageCommitted) error
}

type pluginCommittedOwnerResolver interface {
	Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error)
}

// pluginCommittedRouter runs plugin PersistAfter side effects only on the channel owner node.
type pluginCommittedRouter struct {
	localNodeID   uint64
	channelLog    pluginCommittedOwnerResolver
	nodeClient    pluginCommittedSubmitter
	pluginUsecase pluginPersistAfterUsecase
	logger        wklog.Logger
}

func (r pluginCommittedRouter) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if r.pluginUsecase == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithoutCancel(ctx)
	ownerNodeID, err := r.pluginCommittedOwner(event.Message)
	if err != nil {
		r.logPluginCommittedRoute(event, "owner_status_failed", 0, err)
		return nil
	}
	if ownerNodeID == 0 {
		r.logPluginCommittedRoute(event, "owner_unknown", 0, nil)
		return nil
	}
	if ownerNodeID == r.localNodeID {
		r.logPluginCommittedRoute(event, "local_owner", ownerNodeID, nil)
		if err := r.pluginUsecase.PersistAfterCommitted(ctx, event.Clone()); err != nil {
			r.logPluginCommittedRoute(event, "local_persist_after_failed", ownerNodeID, err)
		}
		return nil
	}
	if r.nodeClient == nil {
		r.logPluginCommittedRoute(event, "node_client_required", ownerNodeID, errPluginCommittedNodeClientRequired)
		return nil
	}
	if err := r.nodeClient.SubmitPluginCommitted(ctx, ownerNodeID, event.Clone()); err != nil {
		r.logPluginCommittedRoute(event, "remote_owner_submit_failed", ownerNodeID, err)
		return nil
	}
	r.logPluginCommittedRoute(event, "remote_owner", ownerNodeID, nil)
	return nil
}

func (r pluginCommittedRouter) pluginCommittedOwner(msg channel.Message) (uint64, error) {
	if r.channelLog == nil {
		return r.localNodeID, nil
	}
	status, err := r.channelLog.Status(channel.ChannelID{ID: msg.ChannelID, Type: msg.ChannelType})
	if err != nil {
		return 0, err
	}
	return uint64(status.Leader), nil
}

func (r pluginCommittedRouter) logPluginCommittedRoute(event messageevents.MessageCommitted, stage string, ownerNodeID uint64, err error) {
	if r.logger == nil {
		return
	}
	if err == nil && !wklog.DebugEnabled(r.logger) {
		return
	}
	fields := []wklog.Field{
		wklog.Event("plugin.diag.committed_route"),
		wklog.String("stage", stage),
		wklog.String("channelID", event.Message.ChannelID),
		wklog.Int("channelType", int(event.Message.ChannelType)),
		wklog.Uint64("messageID", event.Message.MessageID),
		wklog.Uint64("messageSeq", event.Message.MessageSeq),
	}
	if ownerNodeID != 0 {
		fields = append(fields, wklog.Uint64("ownerNodeID", ownerNodeID))
	}
	if err != nil {
		fields = append(fields, wklog.Error(err))
		r.logger.Warn("plugin committed message routing observed failure", fields...)
		return
	}
	r.logger.Debug("plugin committed message routed", fields...)
}
