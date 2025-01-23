package track

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
)

type Position uint16

const (
	PositionUnknown = iota
	// 开始
	PositionStart
	// 提按
	PositionPropose
	// 副本同步
	PositionSync
	// 返回副本同步
	PositionSyncResp
)

func (p Position) String() string {
	switch p {
	case PositionStart:
		return "Start"
	case PositionPropose:
		return "Propose"
	case PositionSync:
		return "Sync"
	case PositionSyncResp:
		return "SyncResp"
	default:
		return "Unknown"
	}
}

type Record struct {
	Path     uint16     // 消息路径
	Cost     [16]uint16 // 耗时记录
	PreStart time.Time  // 上一个开始时间
}

func (m *Record) Add(p Position) {
	m.Path = m.Path | uint16(1<<(16-p))
	m.Cost[(16 - p)] = uint16(time.Since(m.PreStart).Milliseconds())
	m.PreStart = time.Now()
}

func (m *Record) Size() uint64 {
	return 42
}

func (m *Record) HasData() bool {
	return m.Path > 0
}

func (m *Record) Clone() Record {
	return Record{
		Path:     m.Path,
		Cost:     m.Cost,
		PreStart: m.PreStart,
	}
}

// Bytes 方法将 Record 序列化为字节数组
func (m *Record) Encode() []byte {
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

func (m *Record) Decode(data []byte) error {
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

// String 方法，返回消息的字符串表示
func (m *Record) String() string {
	// 构造路径的二进制表示
	pathStr := fmt.Sprintf("%016b", m.Path)

	// 从最高位开始判断path的每一位是否为1，如果是则表示该位置有耗时
	costStr := ""
	totalCost := 0
	rlen := 16
	for i := 0; i < rlen; i++ {
		pos := i + 1
		if m.Path&(1<<uint(rlen-pos)) > 0 {
			cost := m.Cost[rlen-pos]
			totalCost += int(cost)
			costStr += fmt.Sprintf("%s: %dms, ", Position(pos), cost)
		}
	}

	if len(costStr) > 0 {
		// 移除最后一个逗号和空格
		costStr = costStr[:len(costStr)-2]
	}

	// 返回消息的完整字符串表示
	return fmt.Sprintf("Cost: %dms, %s Path: %s", totalCost, costStr, pathStr)
}
