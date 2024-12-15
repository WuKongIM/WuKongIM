package server

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

func (p *processUser) handlePing(msg *reactor.UserMessage) {
	reactor.User.ConnWrite(msg.Conn, &wkproto.PongPacket{})
}
