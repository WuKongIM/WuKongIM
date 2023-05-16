package wkstore

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	lru "github.com/hashicorp/golang-lru/v2"
)

var segmentCache *lru.Cache[string, *segment]

func init() {
	var err error
	segmentCache, err = lru.NewWithEvict(100, func(key string, value *segment) {
		value.release()
	})
	if err != nil {
		panic(err)
	}
}

type FileStoreForMsg struct {
	cfg     *StoreConfig
	slotMap map[uint32]*slot
	wklog.Log
}

func NewFileStoreForMsg(cfg *StoreConfig) *FileStoreForMsg {

	f := &FileStoreForMsg{
		cfg:     cfg,
		slotMap: make(map[uint32]*slot),
		Log:     wklog.NewWKLog("FileStoreForMsg"),
	}
	return f
}

func (f *FileStoreForMsg) AppendMessages(topic string, msgs []Message) ([]uint32, error) {
	return f.getTopic(topic).appendMessages(msgs)
}

func (f *FileStoreForMsg) LoadMsg(topic string, seq uint32) (Message, error) {
	return nil, nil
}

func (f *FileStoreForMsg) LoadNextMsgs(topic string, seq uint32, limit int) ([]Message, error) {

	return nil, nil
}

func (f *FileStoreForMsg) LoadPrevMsgs(topic string, seq uint32, limit int) ([]Message, error) {
	return nil, nil
}

func (f *FileStoreForMsg) LoadRangeMsgs(topic string, start, end uint32) ([]Message, error) {
	return nil, nil
}

func (f *FileStoreForMsg) Close() error {
	if len(f.slotMap) == 0 {
		return nil
	}
	for _, s := range f.slotMap {
		s.close()
	}
	return nil
}

func (f *FileStoreForMsg) getTopic(topic string) *topic {
	slotNum := wkutil.GetSlotNum(f.cfg.SlotNum, topic)
	slot := f.slotMap[slotNum]
	if slot == nil {
		slot = newSlot(slotNum, f.cfg)
		f.slotMap[slotNum] = slot
	}
	return slot.getTopic(topic)
}
