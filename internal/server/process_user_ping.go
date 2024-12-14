package server

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

func (p *processUser) handlePing(msg *reactor.UserMessage) {
	fmt.Println("handlePing---->")
	reactor.User.ConnWrite(msg.Conn, &wkproto.PongPacket{})
}
