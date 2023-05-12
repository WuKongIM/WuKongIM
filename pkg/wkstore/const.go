package wkstore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	fileFormat    = "%020d%s"
	segmentSuffix = ".log"
	indexSuffix   = ".index"
)

// FileDefaultMode FileDefaultMode
const FileDefaultMode os.FileMode = 0755

type SegmentMode int

const (
	SegmentModeAll SegmentMode = iota
	SegmentModeRead
)

var (
	// Encoding Encoding
	Encoding = binary.BigEndian
	// ErrorNotData ErrorNotData
	ErrorNotData = errors.New("no data")

	// MagicNumber MagicNumber
	MagicNumber = [2]byte{0x15, 0x16} // lm
	// EndMagicNumber EndMagicNumber
	EndMagicNumber = [1]byte{0x3}
	// MessageVersion log version
	MessageVersion = [1]byte{0x01}
	// SnapshotMagicNumber SnapshotMagicNumber
	SnapshotMagicNumber = [2]byte{0xb, 0xa} // ba
	// EndSnapshotMagicNumber EndSnapshotMagicNumber
	EndSnapshotMagicNumber = [1]byte{0xf}
	// BackupSlotMagicNumber BackupSlotMagicNumber
	BackupSlotMagicNumber = [2]byte{0xc, 0xd}
	// BackupMagicNumber BackupMagicNumber
	BackupMagicNumber = []byte("---backup start ---")
	// BackupMagicEndNumber BackupMagicEndNumber

	// OffsetSize OffsetSize
	OffsetSize = 8
	// LogDataLenSize LogDataLenSize
	MessageDataLenSize = 4
	// AppliIndexSize AppliIndexSize
	AppliIndexSize           = 8
	IndexMaxSizeOfByte int64 = 2 * 1024 * 1024 // 索引文件的最大大小 2M

)

// a message min len
func getMinMessageLen() int {

	return len(MagicNumber) + len(MessageVersion) + MessageDataLenSize + OffsetSize + AppliIndexSize + len(EndMagicNumber)
}

// next message is vaild if return next message start position
func nextMessageIsVail(reader io.ReaderAt, startOffset int64) (int, error) {
	offset := startOffset

	startMagic := make([]byte, len(MagicNumber))
	// min
	minN, err := reader.ReadAt(startMagic, offset)
	if err != nil {
		return minN, err
	}
	offset += int64(len(MagicNumber))

	// start magic
	if !bytes.Equal(startMagic, MagicNumber[:]) {
		return minN, fmt.Errorf("start MagicNumber不正确 expect:%s actual:%s", string(MagicNumber[:]), string(startMagic))
	}

	offset += int64(len(MessageVersion))

	dataLenBytes := make([]byte, MessageDataLenSize)
	dataLenN, err := reader.ReadAt(dataLenBytes, offset)
	if err != nil {
		return minN + dataLenN, err
	}
	dataLen := Encoding.Uint32(dataLenBytes)

	offset = startOffset + int64(getMinMessageLen()+int(dataLen)-len(EndMagicNumber))

	endMagic := make([]byte, len(EndMagicNumber))
	endMagicN, err := reader.ReadAt(endMagic, offset)
	if err != nil {
		return minN + dataLenN + endMagicN, err
	}

	// start magic
	if !bytes.Equal(endMagic, EndMagicNumber[:]) {
		return minN + dataLenN + endMagicN, fmt.Errorf("end MagicNumber不正确 expect:%s actual:%s", string(EndMagicNumber[:]), string(endMagic))
	}
	return getMinMessageLen() + int(dataLen), nil

}

// 解码 message seq（序号）
func decodeMessageSeq(reader io.ReaderAt, position int64) (offset int64, dataLen int, err error) {
	sizeByte := make([]byte, MessageDataLenSize)
	if _, err = reader.ReadAt(sizeByte, position+int64(len(MagicNumber)+len(MessageVersion))); err != nil {
		return
	}
	offsetByte := make([]byte, OffsetSize)
	if _, err = reader.ReadAt(offsetByte, position+int64(len(MagicNumber)+len(MessageVersion)+MessageDataLenSize)); err != nil {
		return
	}
	dataLen = int(Encoding.Uint32(sizeByte))
	offset = int64(Encoding.Uint64(offsetByte))
	return
}
func roundDown(total, factor int64) int64 {
	return factor * (total / factor)
}
