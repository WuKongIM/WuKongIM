package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	IdSize          = 8                                                // uint64 占用 8 字节
	UidLenSize      = 2                                                // uint16 表示 Uid 长度
	TokenLenSize    = 2                                                // uint16 表示 Token 长度
	BodyLenSize     = 4                                                // uint32 表示 Body 长度
	ConnectMinSize  = IdSize + UidLenSize + TokenLenSize + BodyLenSize // connect最小数据大小
	StatusSize      = 1                                                // uint8 占用 1 字节
	PathLenSize     = 2
	RequestMinSize  = IdSize + PathLenSize + BodyLenSize                    // // request最小数据大小
	ConnackMinSize  = IdSize + StatusSize + BodyLenSize                     // 最小数据大小
	TimestampSize   = 8                                                     // int64 占用 8 字节
	ResponseMinSize = IdSize + StatusSize + TimestampSize + BodyLenSize     // Response最小数据大小
	MsgTypeSize     = 4                                                     // uint32 占用 4 字节
	ContentLenSize  = 4                                                     // uint32 表示 Content 长度
	MessageMinSize  = IdSize + MsgTypeSize + TimestampSize + ContentLenSize // 最小数据大小
)

// 定义 Status 类型
type Status uint8

const (
	StatusOK       Status = 0 // 成功
	StatusError    Status = 1 // 错误
	StatusNotFound Status = 2 // 未找到
)

type Request struct {
	Id   uint64
	Path string
	Body []byte
}

func (r *Request) Reset() {
	r.Id = 0
	r.Path = ""
	r.Body = r.Body[:0]
}

func (r *Request) Marshal() ([]byte, error) {
	pathLen := len(r.Path)
	bodyLen := len(r.Body)
	totalSize := IdSize + PathLenSize + pathLen + BodyLenSize + bodyLen

	buffer := make([]byte, totalSize)
	offset := 0

	binary.LittleEndian.PutUint64(buffer[offset:], r.Id)
	offset += IdSize

	binary.LittleEndian.PutUint16(buffer[offset:], uint16(pathLen))
	offset += PathLenSize
	copy(buffer[offset:], r.Path)
	offset += pathLen

	binary.LittleEndian.PutUint32(buffer[offset:], uint32(bodyLen))
	offset += BodyLenSize
	copy(buffer[offset:], r.Body)

	return buffer, nil
}

func (r *Request) Unmarshal(data []byte) error {
	if len(data) < RequestMinSize {
		return fmt.Errorf("data too short")
	}

	offset := 0
	r.Id = binary.LittleEndian.Uint64(data[offset:])
	offset += IdSize

	pathLen := binary.LittleEndian.Uint16(data[offset:])
	offset += PathLenSize
	if len(data) < offset+int(pathLen) {
		return fmt.Errorf("invalid path length")
	}
	r.Path = string(data[offset : offset+int(pathLen)])
	offset += int(pathLen)

	bodyLen := binary.LittleEndian.Uint32(data[offset:])
	offset += BodyLenSize
	if len(data) < offset+int(bodyLen) {
		return fmt.Errorf("invalid body length")
	}
	r.Body = data[offset : offset+int(bodyLen)]

	return nil
}

type Response struct {
	Id        uint64
	Status    Status
	Timestamp int64
	Body      []byte
}

// Marshal 将 Response 对象编码为二进制数据
func (r *Response) Marshal() ([]byte, error) {
	bodyLen := len(r.Body)

	// 计算总数据大小
	totalSize := IdSize + StatusSize + TimestampSize + BodyLenSize + bodyLen
	buffer := make([]byte, totalSize) // 分配连续的内存

	offset := 0

	// 写入 Id
	binary.LittleEndian.PutUint64(buffer[offset:], r.Id)
	offset += IdSize

	// 写入 Status
	buffer[offset] = uint8(r.Status)
	offset += StatusSize

	// 写入 Timestamp
	binary.LittleEndian.PutUint64(buffer[offset:], uint64(r.Timestamp))
	offset += TimestampSize

	// 写入 Body 长度和内容
	binary.LittleEndian.PutUint32(buffer[offset:], uint32(bodyLen))
	offset += BodyLenSize
	copy(buffer[offset:], r.Body)

	return buffer, nil
}

// Unmarshal 将二进制数据解码为 Response 对象
func (r *Response) Unmarshal(data []byte) error {
	if len(data) < ResponseMinSize {
		return errors.New("data too short to decode")
	}

	offset := 0

	// 读取 Id
	r.Id = binary.LittleEndian.Uint64(data[offset:])
	offset += IdSize

	// 读取 Status
	r.Status = Status(data[offset])
	offset += StatusSize

	// 读取 Timestamp
	r.Timestamp = int64(binary.LittleEndian.Uint64(data[offset:]))
	offset += TimestampSize

	// 读取 Body 长度和内容
	bodyLen := binary.LittleEndian.Uint32(data[offset:])
	offset += BodyLenSize
	if len(data) < offset+int(bodyLen) {
		return errors.New("invalid Body length")
	}
	r.Body = data[offset : offset+int(bodyLen)]

	return nil
}

type Connect struct {
	Id    uint64
	Uid   string
	Token string
	Body  []byte
}

// Marshal 将 Connect 对象编码为二进制数据
func (c *Connect) Marshal() ([]byte, error) {
	uidLen := len(c.Uid)
	tokenLen := len(c.Token)
	bodyLen := len(c.Body)

	// 计算总数据大小
	totalSize := IdSize + UidLenSize + uidLen + TokenLenSize + tokenLen + BodyLenSize + bodyLen
	buffer := make([]byte, totalSize) // 分配连续的内存

	offset := 0

	// 写入 Id
	binary.LittleEndian.PutUint64(buffer[offset:], c.Id)
	offset += IdSize

	// 写入 Uid 长度和内容
	binary.LittleEndian.PutUint16(buffer[offset:], uint16(uidLen))
	offset += UidLenSize
	copy(buffer[offset:], c.Uid)
	offset += uidLen

	// 写入 Token 长度和内容
	binary.LittleEndian.PutUint16(buffer[offset:], uint16(tokenLen))
	offset += TokenLenSize
	copy(buffer[offset:], c.Token)
	offset += tokenLen

	// 写入 Body 长度和内容
	binary.LittleEndian.PutUint32(buffer[offset:], uint32(bodyLen))
	offset += BodyLenSize
	copy(buffer[offset:], c.Body)

	return buffer, nil
}

// Unmarshal 将二进制数据解码为 Connect 对象
func (c *Connect) Unmarshal(data []byte) error {
	if len(data) < ConnectMinSize {
		return errors.New("data too short to decode")
	}

	offset := 0

	// 读取 Id
	c.Id = binary.LittleEndian.Uint64(data[offset:])
	offset += IdSize

	// 读取 Uid 长度和内容
	uidLen := binary.LittleEndian.Uint16(data[offset:])
	offset += UidLenSize
	if len(data) < offset+int(uidLen) {
		return errors.New("invalid Uid length")
	}
	c.Uid = string(data[offset : offset+int(uidLen)])
	offset += int(uidLen)

	// 读取 Token 长度和内容
	tokenLen := binary.LittleEndian.Uint16(data[offset:])
	offset += TokenLenSize
	if len(data) < offset+int(tokenLen) {
		return errors.New("invalid Token length")
	}
	c.Token = string(data[offset : offset+int(tokenLen)])
	offset += int(tokenLen)

	// 读取 Body 长度和内容
	bodyLen := binary.LittleEndian.Uint32(data[offset:])
	offset += BodyLenSize
	if len(data) < offset+int(bodyLen) {
		return errors.New("invalid Body length")
	}
	c.Body = data[offset : offset+int(bodyLen)]

	return nil
}

type Connack struct {
	Id     uint64
	Status Status
	Body   []byte
}

// Marshal 将 Connack 对象编码为二进制数据
func (c *Connack) Marshal() ([]byte, error) {
	bodyLen := len(c.Body)

	// 计算总数据大小
	totalSize := IdSize + StatusSize + BodyLenSize + bodyLen
	buffer := make([]byte, totalSize) // 分配连续的内存

	offset := 0

	// 写入 Id
	binary.LittleEndian.PutUint64(buffer[offset:], c.Id)
	offset += IdSize

	// 写入 Status
	buffer[offset] = uint8(c.Status)
	offset += StatusSize

	// 写入 Body 长度和内容
	binary.LittleEndian.PutUint32(buffer[offset:], uint32(bodyLen))
	offset += BodyLenSize
	copy(buffer[offset:], c.Body)

	return buffer, nil
}

// Unmarshal 将二进制数据解码为 Connack 对象
func (c *Connack) Unmarshal(data []byte) error {
	if len(data) < ConnackMinSize {
		return errors.New("data too short to decode")
	}

	offset := 0

	// 读取 Id
	c.Id = binary.LittleEndian.Uint64(data[offset:])
	offset += IdSize

	// 读取 Status
	c.Status = Status(data[offset])
	offset += StatusSize

	// 读取 Body 长度和内容
	bodyLen := binary.LittleEndian.Uint32(data[offset:])
	offset += BodyLenSize
	if len(data) < offset+int(bodyLen) {
		return errors.New("invalid Body length")
	}
	c.Body = data[offset : offset+int(bodyLen)]

	return nil
}

type Message struct {
	Id        uint64
	MsgType   uint32
	Content   []byte
	Timestamp uint64
}

func (m *Message) Size() int {

	contentLen := len(m.Content)
	totalSize := IdSize + MsgTypeSize + ContentLenSize + contentLen + TimestampSize

	return totalSize
}

// Marshal 将 Message 对象编码为二进制数据
func (m *Message) Marshal() ([]byte, error) {
	contentLen := len(m.Content)

	// 计算总数据大小
	totalSize := IdSize + MsgTypeSize + TimestampSize + ContentLenSize + contentLen
	buffer := make([]byte, totalSize) // 分配连续的内存

	offset := 0

	// 写入 Id
	binary.LittleEndian.PutUint64(buffer[offset:], m.Id)
	offset += IdSize

	// 写入 MsgType
	binary.LittleEndian.PutUint32(buffer[offset:], m.MsgType)
	offset += MsgTypeSize

	// 写入 Timestamp
	binary.LittleEndian.PutUint64(buffer[offset:], m.Timestamp)
	offset += TimestampSize

	// 写入 Content 长度和内容
	binary.LittleEndian.PutUint32(buffer[offset:], uint32(contentLen))
	offset += ContentLenSize
	copy(buffer[offset:], m.Content)

	return buffer, nil
}

// Unmarshal 将二进制数据解码为 Message 对象
func (m *Message) Unmarshal(data []byte) error {
	if len(data) < MessageMinSize {
		return errors.New("data too short to decode")
	}

	offset := 0

	// 读取 Id
	m.Id = binary.LittleEndian.Uint64(data[offset:])
	offset += IdSize

	// 读取 MsgType
	m.MsgType = binary.LittleEndian.Uint32(data[offset:])
	offset += MsgTypeSize

	// 读取 Timestamp
	m.Timestamp = binary.LittleEndian.Uint64(data[offset:])
	offset += TimestampSize

	// 读取 Content 长度和内容
	contentLen := binary.LittleEndian.Uint32(data[offset:])
	offset += ContentLenSize
	if len(data) < offset+int(contentLen) {
		return errors.New("invalid Content length")
	}
	m.Content = data[offset : offset+int(contentLen)]

	return nil
}
