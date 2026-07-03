package message

import "github.com/WuKongIM/WuKongIM/pkg/protocol/frame"

type SendResult struct {
	MessageID  int64
	MessageSeq uint64
	Reason     frame.ReasonCode
}
