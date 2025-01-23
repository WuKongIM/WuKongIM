package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

func (h *Handler) ping(event *eventbus.Event) {
	conn := event.Conn

	trace.GlobalTrace.Metrics.App().PingCountAdd(1)
	trace.GlobalTrace.Metrics.App().PingBytesAdd(1) // ping的大小就是1

	trace.GlobalTrace.Metrics.App().PongCountAdd(1)
	trace.GlobalTrace.Metrics.App().PongBytesAdd(1)
	eventbus.User.ConnWrite(conn, &wkproto.PongPacket{})
}
