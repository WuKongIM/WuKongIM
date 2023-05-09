package db

type Message struct {
}

type MessageResult struct {
}

type MessageReq struct {
	MessageChan chan []*Message
	ResultChan  chan []*MessageResult
}
