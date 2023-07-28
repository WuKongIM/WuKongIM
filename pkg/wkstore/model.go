package wkstore

import (
	"bytes"
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

const conversationVersion = 0x1

// Conversation Conversation
type Conversation struct {
	UID             string // User UID (user who belongs to the most recent session)
	ChannelID       string // Conversation channel
	ChannelType     uint8
	UnreadCount     int    // Number of unread messages
	Timestamp       int64  // Last session timestamp (10 digits)
	LastMsgSeq      uint32 // Sequence number of the last message
	LastClientMsgNo string // Last message client number
	LastMsgID       int64  // Last message ID
	Version         int64  // Data version
}

func (c *Conversation) String() string {
	return fmt.Sprintf("uid:%s channelID:%s channelType:%d unreadCount:%d timestamp: %d lastMsgSeq:%d lastClientMsgNo:%s lastMsgID:%d version:%d", c.UID, c.ChannelID, c.ChannelType, c.UnreadCount, c.Timestamp, c.LastMsgSeq, c.LastClientMsgNo, c.LastMsgID, c.Version)
}

type ConversationSet []*Conversation

func (c ConversationSet) Encode() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	for _, cn := range c {
		enc.WriteUint8(conversationVersion)
		enc.WriteString(cn.UID)
		enc.WriteString(cn.ChannelID)
		enc.WriteUint8(cn.ChannelType)
		enc.WriteInt32(int32(cn.UnreadCount))
		enc.WriteInt64(cn.Timestamp)
		enc.WriteUint32(cn.LastMsgSeq)
		enc.WriteString(cn.LastClientMsgNo)
		enc.WriteInt64(cn.LastMsgID)
		enc.WriteInt64(cn.Version)
	}
	return enc.Bytes()
}

func NewConversationSet(data []byte) ConversationSet {
	conversationSet := ConversationSet{}
	decoder := wkproto.NewDecoder(data)

	for {
		conversation, err := decodeConversation(decoder)
		if err == io.EOF {
			break
		}
		conversationSet = append(conversationSet, conversation)
	}
	return conversationSet
}

func decodeConversation(decoder *wkproto.Decoder) (*Conversation, error) {
	// proto version
	_, err := decoder.Uint8()
	if err != nil {
		return nil, err
	}
	cn := &Conversation{}

	if cn.UID, err = decoder.String(); err != nil {
		return nil, err
	}
	if cn.ChannelID, err = decoder.String(); err != nil {
		return nil, err
	}
	if cn.ChannelType, err = decoder.Uint8(); err != nil {
		return nil, err
	}
	var unreadCount uint32
	if unreadCount, err = decoder.Uint32(); err != nil {
		return nil, err
	}
	cn.UnreadCount = int(unreadCount)

	if cn.Timestamp, err = decoder.Int64(); err != nil {
		return nil, err
	}
	if cn.LastMsgSeq, err = decoder.Uint32(); err != nil {
		return nil, err
	}
	if cn.LastClientMsgNo, err = decoder.String(); err != nil {
		return nil, err
	}
	if cn.LastMsgID, err = decoder.Int64(); err != nil {
		return nil, err
	}
	if cn.Version, err = decoder.Int64(); err != nil {
		return nil, err
	}
	return cn, nil
}

var (
	StreamVersion = [1]byte{0x01}

	// StreamMagicNumber StreamMagicNumber
	StreamMagicNumber = [2]byte{0x15, 0x16}

	// StreamEndMagicNumber StreamEndMagicNumber
	StreamEndMagicNumber = [1]byte{0x3}
)

type StreamMeta struct {
	StreamNo    string             `json:"stream_no"`
	MessageID   int64              `json:"message_id"`
	ChannelID   string             `json:"channel_id"`
	ChannelType uint8              `json:"channel_type"`
	MessageSeq  uint32             `json:"message_seq"`
	StreamFlag  wkproto.StreamFlag `json:"stream_flag"`
}

func (s *StreamMeta) Encode() []byte {
	return []byte(wkutil.ToJSON(s))
}

func (s *StreamMeta) Decode(data []byte) error {

	return wkutil.ReadJSONByByte(data, s)
}

type StreamItem struct {
	ClientMsgNo string
	StreamSeq   uint32
	Blob        []byte
}

func EncodeStreamItem(s *StreamItem) []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteBytes(StreamMagicNumber[:])
	enc.WriteBytes(StreamVersion[:])
	enc.WriteString(s.ClientMsgNo)
	enc.WriteUint32(s.StreamSeq)
	enc.WriteUint32(uint32(len(s.Blob)))
	enc.WriteBytes(s.Blob)
	enc.WriteBytes(StreamEndMagicNumber[:])
	return enc.Bytes()
}

func DecodeStreamItem(data []byte) (*StreamItem, error) {
	decoder := wkproto.NewDecoder(data)
	// magic number
	startMagicBytes, err := decoder.Bytes(len(StreamMagicNumber))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(startMagicBytes, StreamMagicNumber[:]) {
		return nil, fmt.Errorf("invalid magic number")
	}
	// version
	_, err = decoder.Bytes(len(StreamVersion))
	if err != nil {
		return nil, err
	}
	s := &StreamItem{}
	if s.ClientMsgNo, err = decoder.String(); err != nil {
		return nil, err
	}
	if s.StreamSeq, err = decoder.Uint32(); err != nil {
		return nil, err
	}
	var blobLen uint32
	if blobLen, err = decoder.Uint32(); err != nil {
		return nil, err
	}
	if s.Blob, err = decoder.Bytes(int(blobLen)); err != nil {
		return nil, err
	}
	// end magic number
	endMagicBytes, err := decoder.Bytes(len(StreamEndMagicNumber))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(endMagicBytes, StreamEndMagicNumber[:]) {
		return nil, fmt.Errorf("invalid end magic number")
	}
	return s, nil
}

// 获取当前流的基础信息（不包含流内容）
// 返回stream，流的长度，错误
func currentStreamBaseInfo(reader io.ReaderAt, startOffset int64) (*StreamItem, int, error) {

	return currentStreamItem(reader, startOffset, false)
}

func currentStreamItem(reader io.ReaderAt, startOffset int64, readBlob bool) (*StreamItem, int, error) {
	// 读取流的基础信息
	offset := startOffset

	// magic number
	startMagic := make([]byte, len(StreamMagicNumber))
	// min
	_, err := reader.ReadAt(startMagic, offset)
	if err != nil {
		return nil, 0, err
	}
	offset += int64(len(StreamMagicNumber))

	if !bytes.Equal(startMagic, StreamMagicNumber[:]) {
		return nil, 0, fmt.Errorf("start stream MagicNumber不正确 expect:%s actual:%s", string(StreamMagicNumber[:]), string(startMagic))
	}

	// version
	offset += int64(len(StreamVersion))

	// client msg no
	clientMsgNoLenBytes := make([]byte, wkproto.StringFixLenByteSize)
	_, err = reader.ReadAt(clientMsgNoLenBytes, offset)
	if err != nil {
		return nil, 0, err
	}
	offset += int64(len(clientMsgNoLenBytes))

	clientMsgNoLen := Encoding.Uint16(clientMsgNoLenBytes)

	clientMsgNoBytes := make([]byte, clientMsgNoLen)
	_, err = reader.ReadAt(clientMsgNoBytes, offset)
	if err != nil {
		return nil, 0, err
	}
	offset += int64(len(clientMsgNoBytes))
	clientMsgNo := string(clientMsgNoBytes)

	// stream seq
	var streamSeqBytes = make([]byte, 4)
	_, err = reader.ReadAt(streamSeqBytes, offset)
	if err != nil {
		return nil, 0, err
	}
	streamSeq := Encoding.Uint32(streamSeqBytes)
	offset += int64(len(streamSeqBytes))

	// blob len
	var blobLenBytes = make([]byte, 4)
	_, err = reader.ReadAt(blobLenBytes, offset)
	if err != nil {
		return nil, 0, err
	}
	blobLen := Encoding.Uint32(blobLenBytes)
	offset += int64(len(blobLenBytes))
	// blob
	var blob []byte
	if readBlob {
		blob = make([]byte, blobLen)
		_, err = reader.ReadAt(blob, offset)
		if err != nil {
			return nil, 0, err
		}
	}

	offset += int64(blobLen)

	// end magic number
	endMagicBytes := make([]byte, len(StreamEndMagicNumber))
	_, err = reader.ReadAt(endMagicBytes, offset)
	if err != nil {
		return nil, 0, err
	}

	offset += int64(len(endMagicBytes))

	if !bytes.Equal(endMagicBytes, StreamEndMagicNumber[:]) {
		return nil, 0, fmt.Errorf("invalid end stream magic number")
	}

	return &StreamItem{
		ClientMsgNo: clientMsgNo,
		StreamSeq:   streamSeq,
		Blob:        blob,
	}, int(offset - startOffset), nil
}

type StreamItemSlice []*StreamItem

func (s StreamItemSlice) Len() int {
	return len(s)
}

func (l StreamItemSlice) Less(i, j int) bool {
	return l[i].StreamSeq < l[j].StreamSeq
}

func (l StreamItemSlice) Swap(i, j int) { l[i], l[j] = l[j], l[i] }
