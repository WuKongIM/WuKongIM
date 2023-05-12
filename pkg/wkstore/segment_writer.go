package wkstore

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/pkg/errors"
)

type segmentBase struct {
	baseOffset  int64
	t           *topic
	segmentDir  string
	segmentFile *os.File
}

func newSegmentBase(baseOffset int64, t *topic, flag int) *segmentBase {
	segmentDir := filepath.Join(t.topicDir, "logs")
	s := &segmentBase{
		baseOffset: baseOffset,
		t:          t,
		segmentDir: segmentDir,
	}
	err := os.MkdirAll(s.segmentDir, FileDefaultMode)
	if err != nil {
		panic(err)
	}
	pathStr := s.segmentPath()
	s.segmentFile, err = os.OpenFile(pathStr, flag, FileDefaultMode)
	if err != nil {
		panic(err)
	}
	return s
}

func (s *segmentBase) segmentPath() string {
	return filepath.Join(s.segmentDir, fmt.Sprintf(fileFormat, s.baseOffset, segmentSuffix))
}
func (s *segmentBase) backupPath(t int64) string {
	return filepath.Join(s.segmentDir, fmt.Sprintf(fileFormat, s.baseOffset, fmt.Sprintf("%s.bak%d", segmentSuffix, t)))
}

type segmentWriter struct {
	segmentBase
	wklog.Log
	sync.RWMutex
}

func newSegmentWriter(baseOffset int64, t *topic) *segmentWriter {
	s := &segmentWriter{
		segmentBase: *newSegmentBase(baseOffset, t, os.O_RDWR|os.O_CREATE|os.O_APPEND),
	}

	s.Log = wklog.NewWKLog(fmt.Sprintf("segment[%s]", s.segmentPath()))

	return s
}

func (s *segmentWriter) appendMessages(msgs []Message) ([]uint32, error) {

	seqs := make([]uint32, 0)
	for _, msg := range msgs {
		seq, err := s.appendMessage(msg)
		if err != nil {
			return nil, err
		}
		seqs = append(seqs, seq)
	}

	return seqs, nil
}
func (s *segmentWriter) appendMessage(msg Message) (uint32, error) {

	return 0, nil
}

func (s *segmentWriter) append(data []byte) (int, error) {
	n, err := s.segmentFile.Write(data)
	if err != nil {
		return 0, errors.Wrap(err, "log write failed")
	}
	return n, nil
}
