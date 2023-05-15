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

func (f *FileStore) GetChannel(channelID string, channelType uint8) (*ChannelInfo, error) {
	return nil, nil
}

func (f *FileStore) GetUserToken(uid string, deviceFlag uint8) (string, uint8, error) {
	return "", 0, nil
}

func (f *FileStore) AppendMessageOfNotifyQueue(m []Message) error {
	return nil
}

func (f *FileStore) AddOrUpdateChannel(channelInfo *ChannelInfo) error {
	return nil
}

func (f *FileStore) ExistChannel(channelID string, channelType uint8) (bool, error) {
	return false, nil
}

func (f *FileStore) AddSubscribers(channelID string, channelType uint8, uids []string) error {
	return nil
}

func (f *FileStore) RemoveSubscribers(channelID string, channelType uint8, uids []string) error {
	return nil
}

func (f *FileStore) GetSubscribers(channelID string, channelType uint8) ([]string, error) {
	return nil, nil
}

func (f *FileStore) RemoveAllSubscriber(channelID string, channelType uint8) error {
	return nil
}

func (f *FileStore) GetAllowlist(channelID string, channelType uint8) ([]string, error) {
	return nil, nil
}

func (f *FileStore) GetDenylist(channelID string, channelType uint8) ([]string, error) {
	return nil, nil
}

func (f *FileStore) DeleteChannel(channelID string, channelType uint8) error {
	return nil
}

func (f *FileStore) AddSystemUIDs(uids []string) error {
	return nil
}

func (f *FileStore) RemoveSystemUIDs(uids []string) error {
	return nil
}

func (f *FileStore) GetSystemUIDs() ([]string, error) {
	return nil, nil
}

func (f *FileStore) AddOrUpdateConversations(uid string, conversations []*Conversation) error {
	return nil
}

func (f *FileStore) GetConversations(uid string) ([]*Conversation, error) {
	return nil, nil
}

func (f *FileStore) GetConversation(uid string, channelID string, channelType uint8) (*Conversation, error) {
	return nil, nil
}
func (f *FileStore) DeleteConversation(uid string, channelID string, channelType uint8) error {
	return nil
}

func (f *FileStore) AppendMessagesOfUser(messages []Message) error {
	return nil
}

func (f *FileStore) GetUserNextMessageSeq(uid string) (uint32, error) {
	return 0, nil
}

func (f *FileStore) RemoveMessagesOfNotifyQueue(messageIDs []int64) error {
	return nil
}

func (f *FileStore) GetMessagesOfNotifyQueue(count int) ([]Message, error) {
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
