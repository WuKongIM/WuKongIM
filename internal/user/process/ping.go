package process

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

func (p *User) handlePing(msg *reactor.UserMessage) {
	reactor.User.ConnWrite(msg.Conn, &wkproto.PongPacket{})
}
