package cluster

import (
	"path"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/wal"
)

type slotLog struct {
	slotID  uint32
	dataDir string
	walLog  *wal.Log
}

func newSlotLog(slotID uint32, dataDir string) *slotLog {
	return &slotLog{
		slotID:  slotID,
		dataDir: dataDir,
	}
}

func (s *slotLog) Open() error {
	var err error
	s.walLog, err = wal.Open(s.walPath(), wal.DefaultOptions)
	return err
}

func (s *slotLog) Close() error {

	return s.walLog.Close()
}

func (s *slotLog) walPath() string {
	return path.Join(s.dataDir, "wal.log")
}

func (s *slotLog) Append(logIndex uint64, data []byte) error {
	return s.walLog.Write(logIndex, data)
}

func (s *slotLog) LastIndex() (uint64, error) {
	return s.walLog.LastIndex()
}
