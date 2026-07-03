package app

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/legacy/usecase/benchdata"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/channel"
	userusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// benchUserWriter adapts bench-local token DTOs to the user usecase boundary.
type benchUserWriter struct {
	users interface {
		UpdateToken(context.Context, userusecase.UpdateTokenCommand) error
	}
}

func (w benchUserWriter) UpdateToken(ctx context.Context, cmd benchdata.UserTokenCommand) error {
	return w.users.UpdateToken(ctx, userusecase.UpdateTokenCommand{
		UID:         cmd.UID,
		Token:       cmd.Token,
		DeviceFlag:  frame.DeviceFlag(cmd.DeviceFlag),
		DeviceLevel: frame.DeviceLevel(cmd.DeviceLevel),
	})
}

// benchChannelWriter adapts bench-local channel DTOs to the channel usecase boundary.
type benchChannelWriter struct {
	channels interface {
		Upsert(context.Context, channelusecase.UpsertCommand) error
		AddSubscribers(context.Context, channelusecase.SubscriberCommand) error
	}
}

func (w benchChannelWriter) UpsertChannel(ctx context.Context, ch benchdata.ChannelRecord) error {
	return w.channels.Upsert(ctx, channelusecase.UpsertCommand{
		Info: channelusecase.Info{
			ChannelID:     ch.ChannelID,
			ChannelType:   ch.ChannelType,
			Large:         ch.Large,
			Ban:           ch.Ban,
			Disband:       ch.Disband,
			SendBan:       ch.SendBan,
			AllowStranger: ch.AllowStranger,
		},
	})
}

func (w benchChannelWriter) AddSubscribers(ctx context.Context, channelID string, channelType uint8, uids []string) error {
	return w.channels.AddSubscribers(ctx, channelusecase.SubscriberCommand{
		ChannelID:   channelID,
		ChannelType: channelType,
		Subscribers: append([]string(nil), uids...),
	})
}
