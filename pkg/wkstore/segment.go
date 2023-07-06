package wkstore

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type segment struct {
	cfg            *StoreConfig
	baseMessageSeq uint32
	segmentDir     string
	wklog.Log
	segmentFile   *os.File    // message segment file
	position      uint32      // current write position
	isSanityCheck atomic.Bool // sanity check
	lastMsgSeq    atomic.Uint32
	fileSize      int64
	index         *Index
	sync.RWMutex

	indexIntervalBytes       int64 // index interval bytes
	bytesSinceLastIndexEntry int64 // number of bytes written since the last index entry
	topic                    *topic
}

func newSegment(topic *topic, baseMessageSeq uint32, cfg *StoreConfig) *segment {
	segmentDir := filepath.Join(topic.topicDir, "logs")
	s := &segment{
		cfg:                cfg,
		segmentDir:         segmentDir,
		baseMessageSeq:     baseMessageSeq,
		indexIntervalBytes: 4 * 1024,
		topic:              topic,
	}
	err := os.MkdirAll(s.segmentDir, FileDefaultMode)
	if err != nil {
		s.Error("mkdir segment dir fail", zap.Error(err))
		panic(err)
	}
	s.Log = wklog.NewWKLog(fmt.Sprintf("segment[%s]", s.segmentPath()))
	s.index = NewIndex(s.indexPath(), baseMessageSeq)

	return s
}

func (s *segment) appendMessages(msgs []Message) (int, error) {
	if len(msgs) == 0 {
		return 0, nil
	}

	firstData := msgs[0].Encode()

	var msgData []byte
	for i := 1; i < len(msgs); i++ {
		msgData = msgs[i].Encode()
		firstData = append(firstData, msgData...)
	}

	n, err := s.append(firstData)
	if err != nil {
		return 0, err
	}
	if s.bytesSinceLastIndexEntry > s.indexIntervalBytes || len(firstData) > int(s.indexIntervalBytes) {
		err = s.index.Append(msgs[0].GetSeq(), s.position-uint32(n))
		if err != nil {
			return 0, err
		}
		s.bytesSinceLastIndexEntry = 0
	}
	s.bytesSinceLastIndexEntry += int64(n)
	return n, nil
}

func (s *segment) append(data []byte) (int, error) {
	s.Lock()
	defer s.Unlock()
	n, err := s.segmentFile.Write(data)
	if err != nil {
		return 0, errors.Wrap(err, "log write failed")
	}
	s.position += uint32(n)
	return n, nil
}

// readMessages readMessages
func (s *segment) readMessages(messageSeq uint32, limit uint64, callback func(msg Message) error) error {
	s.RLock()
	defer s.RUnlock()
	messageSeqPosition, err := s.index.Lookup(messageSeq)
	if err != nil {
		s.Error("readMessages-index.Lookup is error", zap.Error(err))
		return err
	}
	var startPosition int64 = 0
	if messageSeqPosition.MessageSeq == messageSeq {
		startPosition = messageSeqPosition.Position
	} else {
		startPosition, _, err = s.readTargetPosition(messageSeqPosition.Position, messageSeq)
		if err != nil {
			if errors.Is(err, ErrorNotData) {
				s.Debug("未读取到数据！")
				return nil
			}
			s.Error("readMessages-readTargetPosition is error", zap.Error(err))
			return err
		}
	}
	err = s.readMessagesAtPosition(startPosition, limit, callback)
	if err != nil {
		s.Error("readMessages.readLogsAtPosition is error", zap.Error(err), zap.Int64("startPosition", startPosition), zap.Uint32("epectMessage", messageSeq), zap.Int64("actMessageSeq", int64(messageSeqPosition.MessageSeq)), zap.Int64("Position", messageSeqPosition.Position))
		return err
	}
	return nil
}

// readMessageAtPosition 在文件指定开始位置，读取指定数量[limit]的日志数据
func (s *segment) readMessagesAtPosition(position int64, limit uint64, callback func(m Message) error) error {
	var count uint64 = 0
	var startPosition = position
	for {
		if startPosition >= s.getFileSize() || count >= limit {
			// s.Info("startPosition已超过文件大小！", zap.Int64("", startPosition), zap.Int64("s.getFileSize()", s.getFileSize()), zap.Int("count", int(count)), zap.Uint64("limit", limit))
			break
		}
		lg, msgSize, err := decodeMessageAt(s.segmentFile, startPosition, s.cfg.DecodeMessageFnc) // 解码日志
		// data, startPosition, err = s.readLogDataAtPosition(startPosition)
		if err != nil {
			return err
		}
		startPosition = startPosition + int64(msgSize)
		if callback != nil {
			err = callback(lg)
			if err != nil {
				return err
			}
		}
		count++
	}
	return nil
}

// 获取目标offset的文件位置
func (s *segment) readTargetPosition(startPosition int64, targetMessageSeq uint32) (int64, int64, error) {

	if startPosition >= s.getFileSize() {
		s.Debug("当前文件位置大于文件本身大小", zap.Int64("startPosition", startPosition), zap.Int64("fileSize", s.getFileSize()), zap.Uint32("targetMessageSeq", targetMessageSeq))
		return 0, 0, ErrorNotData
	}
	resultOffset, dataLen, err := decodeMessageSeq(s.segmentFile, startPosition)
	if err != nil {
		s.Error("DecodeLogOffset is error", zap.Error(err))
		return 0, 0, err
	}

	nextStartPosition := startPosition + int64(getMinMessageLen()+dataLen) // 下一条日志的文件开始位置

	if resultOffset == targetMessageSeq {
		return startPosition, nextStartPosition, nil
	}

	targetPosition, nextP, err := s.readTargetPosition(nextStartPosition, targetMessageSeq)
	if err != nil {
		return 0, 0, err
	}
	return targetPosition, nextP, nil
}

// init check segment
func (s *segment) init(mode SegmentMode) error {
	if s.isSanityCheck.Load() {
		return nil
	}
	var err error
	pathStr := s.segmentPath()
	if mode == SegmentModeAll {
		s.segmentFile, err = os.OpenFile(pathStr, os.O_RDWR|os.O_CREATE|os.O_APPEND, FileDefaultMode)
	} else {
		s.segmentFile, err = os.OpenFile(pathStr, os.O_RDONLY|os.O_CREATE, FileDefaultMode)
	}

	if err != nil {
		s.Error("open file fail!", zap.Error(err), zap.String("path", pathStr))
		return err
	}
	fi, err := s.segmentFile.Stat()
	if err != nil {
		return err
	} else if fi.Size() > 0 {
		s.fileSize = fi.Size()
	}

	lastMsgStartPosition, err := s.sanityCheck()
	if err != nil {
		return err
	}
	if lastMsgStartPosition == 0 {
		if s.position > 0 { // // lastMsgStartPosition等0 position大于0 说明有一条消息
			s.lastMsgSeq.Store(uint32(s.baseMessageSeq + 1))
		} else {
			s.lastMsgSeq.Store(uint32(s.baseMessageSeq))
		}
	} else {
		messageSeq, _, err := decodeMessageSeq(s.segmentFile, lastMsgStartPosition)
		if err != nil {
			return err
		}
		s.lastMsgSeq.Store(uint32(messageSeq))
	}
	s.isSanityCheck.Store(true)

	return nil
}

// ReadAt ReadAt
func (s *segment) readAt(messageSeq uint32) (Message, error) {
	s.Lock()
	defer s.Unlock()
	messageSeqPosition, err := s.index.Lookup(messageSeq)
	if err != nil {
		return nil, err
	}
	targetPosition, _, err := s.readTargetPosition(messageSeqPosition.Position, messageSeq)
	if err != nil {
		return nil, err
	}
	m, _, err := decodeMessageAt(s.segmentFile, targetPosition, s.cfg.DecodeMessageFnc)
	return m, err
}

// SanityCheck Sanity check
func (s *segment) sanityCheck() (int64, error) {

	stat, err := s.segmentFile.Stat()
	if err != nil {
		s.Error("Stat file fail!", zap.Error(err))
		panic(err)
	}
	segmentSizeOfByte := stat.Size()

	if segmentSizeOfByte == 0 {
		return 0, nil
	}

	hasEndMgNumer, err := s.hasEndMagicNumer(segmentSizeOfByte)
	if err != nil {
		return 0, err
	}
	if !hasEndMgNumer {
		s.Debug("No magic number at the end,sanity check mode is full")
		return s.sanityFullCheck(segmentSizeOfByte)
	}
	if segmentSizeOfByte <= int64(s.cfg.EachMessagegMaxSizeOfBytes)+int64(getMinMessageLen()) {
		s.Debug("File is too small,sanity check mode is full")
		return s.sanityFullCheck(segmentSizeOfByte)
	}

	s.Debug("sanity check mode is simple")
	check, lastMsgStartPosition, err := s.sanitySimpleCheck(segmentSizeOfByte)
	if err != nil {
		s.Warn("sanitySimpleCheck is error，start sanityFullCheck", zap.Error(err))
		return s.sanityFullCheck(segmentSizeOfByte)
	}
	if !check {
		s.Debug("sanity check simple mode is fail！Turn on full mode")
		return s.sanityFullCheck(segmentSizeOfByte)
	}
	return lastMsgStartPosition, nil
}

// 返回最后一条日志的开始位置
func (s *segment) sanitySimpleCheck(segmentSizeOfByte int64) (bool, int64, error) {

	assertDataSize := int64(s.cfg.EachMessagegMaxSizeOfBytes + getMinMessageLen()) // assert last message size
	if assertDataSize >= segmentSizeOfByte {
		s.Debug("assertDataSize >  segmentSizeOfByte") // if return false will go to fullCheck
		return false, 0, nil
	}

	offsetPosistion := s.index.LastPosition()

	if offsetPosistion.Position <= 0 && offsetPosistion.MessageSeq <= 0 {
		return false, 0, nil
	}

	startCheckPosition := offsetPosistion.Position

	fmt.Println("startCheckPosition----->", startCheckPosition, segmentSizeOfByte)

	lastMsgLen := 0 // last message len
	for {
		if startCheckPosition >= segmentSizeOfByte {
			break
		}

		readLen, err := nextMessageIsVail(s.segmentFile, startCheckPosition)
		if err != nil {
			if errors.Is(err, io.EOF) { // 无内容了
				break
			}
			startCheckPosition += int64(readLen)
			continue
		}
		if readLen == 0 {
			break
		}
		lastMsgLen = readLen
		startCheckPosition += int64(readLen)

	}
	if lastMsgLen == 0 {
		s.Debug("lastMsgLen is  0")
		return false, 0, nil
	}
	s.position = uint32(startCheckPosition)

	return true, startCheckPosition - int64(lastMsgLen), nil
}

func (s *segment) sanityFullCheck(segmentSizeOfByte int64) (int64, error) {

	return s.check(segmentSizeOfByte)
}

func (s *segment) hasEndMagicNumer(segmentSizeOfByte int64) (bool, error) {
	var p = make([]byte, 1)
	_, err := s.segmentFile.ReadAt(p, segmentSizeOfByte-1)
	if err != nil {
		return false, err
	}
	return bytes.Equal(p, EndMagicNumber[:]), nil
}

// 检查消息文件的有效性。
// keepCorrect 是否保持消息文件的有效果性，如果为true 将删除掉无效的消息字节
func (s *segment) check(segmentSizeOfByte int64) (int64, error) {
	// _, err := s.logFile.Seek(startPosition, io.SeekStart)
	// if err != nil {
	// 	return 0, err
	// }
	var checkPosition int64 = 0 // 文件开始检查的位置

	var err error
	var vailMsgLen uint32 = 0 // 整个消息的有效长度
	lastMsgLen := 0           // 最后一条消息长度
	for {
		if vailMsgLen >= uint32(segmentSizeOfByte) {
			break
		}
		len, err := nextMessageIsVail(s.segmentFile, checkPosition)
		if err != nil {
			break

		}
		checkPosition += int64(len)
		lastMsgLen = len
		vailMsgLen += uint32(len)
	}
	s.position = vailMsgLen
	if s.position != uint32(segmentSizeOfByte) {
		s.Warn("Back up the original log and remove the damaged log")
		err = s.backup()
		if err != nil {
			s.Error("backup fail!", zap.Error(err))
			return 0, err
		}

		err = s.segmentFile.Truncate(int64(s.position))
		if err != nil {
			s.Error("truncate fail", zap.Error(err))
			return 0, err
		}
		_, err := s.segmentFile.Seek(int64(s.position), io.SeekStart)
		if err != nil {
			return 0, err
		}
	}
	return int64(vailMsgLen) - int64(lastMsgLen), nil
}
func (s *segment) getFileSize() int64 {
	if s.position == 0 {
		return s.fileSize
	}
	return int64(s.position)
}

// sync
func (s *segment) sync() error {
	return s.segmentFile.Sync()
}

func (s *segment) close() error {
	err := s.sync()
	s.segmentFile.Close()
	s.index.Close()
	return err
}

func (s *segment) backup() error {
	_, err := wkutil.CopyFile(s.backupPath(time.Now().UnixNano()), s.segmentPath())
	return err
}

func (s *segment) release() {
	s.close()
}
func (s *segment) segmentPath() string {
	return filepath.Join(s.segmentDir, fmt.Sprintf(fileFormat, s.baseMessageSeq, segmentSuffix))
}
func (s *segment) backupPath(t int64) string {
	return filepath.Join(s.segmentDir, fmt.Sprintf(fileFormat, s.baseMessageSeq, fmt.Sprintf("%s.bak%d", segmentSuffix, t)))
}
func (s *segment) indexPath() string {
	return filepath.Join(s.segmentDir, fmt.Sprintf(fileFormat, s.baseMessageSeq, indexSuffix))
}
