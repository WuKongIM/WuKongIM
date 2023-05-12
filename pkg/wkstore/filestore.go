package wkstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	lru "github.com/hashicorp/golang-lru/v2"
)

type FileStore struct {
	cfg          *StoreConfig
	slotMap      map[uint32]*slot
	segmentCache *lru.Cache[string, *segment]
}

func NewFileStore(cfg *StoreConfig) *FileStore {
	f := &FileStore{
		cfg:     cfg,
		slotMap: make(map[uint32]*slot),
	}
	var err error
	f.segmentCache, err = lru.NewWithEvict(cfg.MaxSegmentCacheNum, func(key string, value *segment) {
		value.release()
	})
	if err != nil {
		panic(err)
	}
	return f
}

func (f *FileStore) Open() error {
	return nil

}

func (f *FileStore) StoreMsg(topic string, msgs []Message) ([]uint32, error) {
	return f.getTopic(topic).appendMessages(msgs)
}

func (f *FileStore) LoadMsg(topic string, seq uint32) (Message, error) {
	return nil, nil
}

func (f *FileStore) LoadNextMsgs(topic string, seq uint32, limit int) ([]Message, error) {

	return nil, nil
}

func (f *FileStore) LoadPrevMsgs(topic string, seq uint32, limit int) ([]Message, error) {
	return nil, nil
}

func (f *FileStore) LoadRangeMsgs(topic string, start, end uint32) ([]Message, error) {
	return nil, nil
}

func (f *FileStore) Close() error {
	if len(f.slotMap) == 0 {
		return nil
	}
	for _, s := range f.slotMap {
		s.close()
	}
	return nil

}

func (f *FileStore) getTopic(topic string) *topic {
	slotNum := wkutil.GetSlotNum(f.cfg.SlotNum, topic)
	slot := f.slotMap[slotNum]
	if slot == nil {
		slot = newSlot(slotNum, f)
		f.slotMap[slotNum] = slot
	}
	return slot.getTopic(topic)
}

type StoreConfig struct {
	SlotNum               int //
	DataDir               string
	MaxSegmentCacheNum    int
	EachLogMaxSizeOfBytes int
	SegmentMaxBytes       int64 // each segment max size of bytes default 2G
}

func NewStoreConfig() *StoreConfig {
	return &StoreConfig{
		SlotNum:               256,
		DataDir:               "./data",
		MaxSegmentCacheNum:    2000,
		EachLogMaxSizeOfBytes: 1024 * 1024 * 2, // 2M
		SegmentMaxBytes:       1024 * 1024 * 1024 * 2,
	}
}
