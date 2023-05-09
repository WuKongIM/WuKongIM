package db

import "context"

type MessageChan chan []*Message

type IDB interface {
	IMessageDB
}

type IMessageDB interface {
	// AppendMessage 追加消息到频道队列
	// messageSeqs 返回的消息messageSeq
	AppendMessages(messages []*Message, ctx context.Context) (messageSeqs []uint32, err error)
}

type FileDB struct {
}
