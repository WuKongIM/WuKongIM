package wkstore

type Store interface {
	Open() error
	Close() error
	// StoreMsg return seqs and error, seqs len is msgs len
	StoreMsg(topic string, msgs []Message) (seqs []uint32, err error)
	LoadMsg(topic string, seq uint32) (Message, error)
	LoadNextMsgs(topic string, seq uint32, limit int) ([]Message, error)
	LoadPrevMsgs(topic string, seq uint32, limit int) ([]Message, error)
	LoadRangeMsgs(topic string, start, end uint32) ([]Message, error)
}
