package app

import (
	"strconv"

	"github.com/WuKongIM/WuKongIM/internal/contracts/sendbanaudit"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// sendBanAuditObserver binds credential-free policy audit events to application
// logs. The mutation result carries old state from atomic apply, never a GET.
func (a *App) sendBanAuditObserver(event sendbanaudit.Event) {
	scope := "channel"
	if event.UID != "" {
		scope = "user"
	}
	fields := []wklog.Field{
		wklog.Event("internal.send_ban.mutation"), wklog.String("scope", scope),
		wklog.String("source", event.Actor.Source), wklog.String("operator", event.Actor.Name), wklog.String("peer", event.Actor.Peer),
		wklog.String("uid", event.UID), wklog.String("channel_id", event.ChannelID), wklog.Int64("channel_type", event.ChannelType),
		wklog.Int64("requested_send_ban", event.Requested), wklog.String("result", event.Result),
		wklog.Bool("policy_known", event.Previous != nil && event.Current != nil),
	}
	if event.Previous != nil && event.Current != nil {
		fields = append(fields, wklog.Int64("previous_send_ban", event.Previous.SendBan), wklog.String("previous_send_ban_version", strconv.FormatUint(event.Previous.Version, 10)),
			wklog.Int64("send_ban", event.Current.SendBan), wklog.String("send_ban_version", strconv.FormatUint(event.Current.Version, 10)))
	}
	a.logger.Info("Send ban mutation", fields...)
}
