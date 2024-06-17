package reactor

import (
	"encoding/binary"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
)

type Message struct {
	HandlerKey string
	replica.Message
}

func (m Message) Marshal() ([]byte, error) {

	msgBytes, err := m.Message.Marshal()
	if err != nil {
		return nil, err
	}
	resultBytes := make([]byte, 2+len(m.HandlerKey)+len(msgBytes))
	binary.BigEndian.PutUint16(resultBytes, uint16(len(m.HandlerKey)))
	copy(resultBytes[2:], []byte(m.HandlerKey))
	copy(resultBytes[2+len(m.HandlerKey):], msgBytes)
	return resultBytes, nil
}

func UnmarshalMessage(data []byte) (Message, error) {
	handleKeyLen := binary.BigEndian.Uint16(data)
	handlerKey := string(data[2 : 2+handleKeyLen])
	msg, err := replica.UnmarshalMessage(data[2+handleKeyLen:])
	if err != nil {
		return Message{}, err
	}
	return Message{
		HandlerKey: handlerKey,
		Message:    msg,
	}, nil
}
