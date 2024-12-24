package track

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
)

type Position uint16

const (
	PositionStart Position = iota
	// 用户收到消息
	PositionUserOnSend
	// 节点转发send消息
	PositionNodeOnSend
	// 频道收到消息
	PositionChannelOnSend
	// 权限检查
	PositionChannelPermission
	// 消息存储
	PositionChannelStorage
	// 发送回执
	PositionChannelSendack
	// 生成标签
	PositionChannelMakeTag
	// 推送在线消息
	PositionPushOnline
	// 在线推送结束
	PositionPushOnlineEnd
	// 推送离线消息
	PositionPushOffline
	// 转发写消息
	PositionForwardConnWrite

	// 消息写
	PositionConnWrite

	// 用户收到消息回执
	PositionUserRecvack
)

func (p Position) String() string {
	switch p {
	case PositionStart:
		return "Start"
	case PositionUserOnSend:
		return "UserOnSend"
	case PositionNodeOnSend:
		return "NodeOnSend"
	case PositionChannelOnSend:
		return "ChannelOnSend"
	case PositionChannelPermission:
		return "ChannelPermission"
	case PositionChannelStorage:
		return "ChannelStorage"
	case PositionChannelSendack:
		return "ChannelSendack"
	case PositionChannelMakeTag:
		return "ChannelMakeTag"
	case PositionPushOnline:
		return "PushOnline"
	case PositionPushOnlineEnd:
		return "PushOnlineEnd"
	case PositionPushOffline:
		return "PushOffline"
	case PositionForwardConnWrite:
		return "ForwardConnWrite"
	case PositionConnWrite:
		return "ConnWrite"
	case PositionUserRecvack:
		return "UserRecvack"
	default:
		return "Unknown"
	}
}

type Message struct {
	Path     uint16     // 消息路径
	Cost     [16]uint16 // 耗时记录
	PreStart time.Time  // 上一个开始时间
}

func (m *Message) Record(p Position) {
	m.Path = m.Path | uint16(1<<(15-p))
	m.Cost[(15 - p)] = uint16(time.Since(m.PreStart).Milliseconds())
	m.PreStart = time.Now()
}

func (m *Message) Size() uint64 {
	return 42
}

func (m *Message) HasData() bool {
	return m.Path > 0
}

func (m *Message) Clone() Message {
	return Message{
		Path:     m.Path,
		Cost:     m.Cost,
		PreStart: m.PreStart,
	}
}

// Bytes 方法将 Message 序列化为字节数组
func (m *Message) Encode() []byte {
	// 创建一个字节缓冲区
	var buf bytes.Buffer
	// 写入 path（uint16 类型，占 2 字节）
	_ = binary.Write(&buf, binary.BigEndian, m.Path)

	// 写入 cost 数组（16 个 uint16，占 32 字节）
	for _, cost := range m.Cost {
		_ = binary.Write(&buf, binary.BigEndian, cost)
	}

	// 写入 preStart（时间戳，int64 类型，占 8 字节）
	preStartTimestamp := m.PreStart.UnixNano() // 转换为纳秒级的 Unix 时间戳
	_ = binary.Write(&buf, binary.BigEndian, preStartTimestamp)

	// 返回字节数组
	return buf.Bytes()
}

func (m *Message) Decode(data []byte) error {
	if len(data) < 42 {
		return fmt.Errorf("data too short")
	}

	buf := bytes.NewReader(data)

	// 读取 path
	if err := binary.Read(buf, binary.BigEndian, &m.Path); err != nil {
		return err
	}

	// 读取 cost 数组
	for i := range m.Cost {
		if err := binary.Read(buf, binary.BigEndian, &m.Cost[i]); err != nil {
			return err
		}
	}

	// 读取 preStart 时间戳
	var preStartTimestamp int64
	if err := binary.Read(buf, binary.BigEndian, &preStartTimestamp); err != nil {
		return err
	}
	m.PreStart = time.Unix(0, preStartTimestamp)

	return nil
}
func (m *Message) String() string {
	// 构造路径的二进制表示
	pathStr := fmt.Sprintf("%016b", m.Path)

	// 初始化变量
	costStr := ""
	totalCost := 0
	rlen := 15
	posList := make([]int, 0, 16)
	costList := make([]uint16, 0, 16)

	// 收集path中为1的位置以及对应的耗时
	for i := rlen - 1; i > 0; i-- {
		pos := rlen - i
		if m.Path&(1<<uint(i)) > 0 {
			posList = append(posList, pos)
			costList = append(costList, m.Cost[i])
		}
	}

	// 错位显示耗时
	cost := uint16(0)
	for i := 0; i < len(posList); i++ {
		pos := posList[i]
		if i == len(posList)-1 {
			cost = 0
		} else {
			cost = costList[i+1] // 取错位的耗时
		}

		totalCost += int(cost)
		costStr += fmt.Sprintf("%s: %dms, ", Position(pos), cost)
	}

	// 移除最后一个逗号和空格
	if len(costStr) > 0 {
		costStr = costStr[:len(costStr)-2]
	}

	// 返回消息的完整字符串表示
	return fmt.Sprintf("Cost: %dms, %s Path: %s", totalCost, costStr, pathStr)
}
