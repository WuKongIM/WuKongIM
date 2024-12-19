package process

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

func (p *User) processPing(msg *reactor.UserMessage) {
	trace.GlobalTrace.Metrics.App().PingCountAdd(1)
	trace.GlobalTrace.Metrics.App().PingBytesAdd(1) // ping的大小就是1

	trace.GlobalTrace.Metrics.App().PongCountAdd(1)
	trace.GlobalTrace.Metrics.App().PongBytesAdd(1)

	reactor.User.ConnWrite(msg.Conn, &wkproto.PongPacket{})

}
