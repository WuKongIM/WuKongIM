package wkstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

const UserQueuePrefix = "userqueue_"

type FileStore struct {
	cfg *StoreConfig
	db  *pebble.DB

	lock *keylock.KeyLock

	wo *pebble.WriteOptions

	*FileStoreForMsg
}

func NewFileStore(cfg *StoreConfig) *FileStore {

	f := &FileStore{
		cfg:             cfg,
		lock:            keylock.NewKeyLock(),
		FileStoreForMsg: NewFileStoreForMsg(cfg),
		wo: &pebble.WriteOptions{
			Sync: true,
		},
	}

	return f
}

func (f *FileStore) Open() error {
	f.lock.StartCleanLoop()
	var err error
	f.db, err = pebble.Open(filepath.Join(f.cfg.DataDir, "wukongmetadb"), &pebble.Options{})
	if err != nil {
		return err
	}
	return err

}
func (f *FileStore) Close() error {
	f.lock.StopCleanLoop()
	f.FileStoreForMsg.Close()
	f.db.Close()
	return nil
}

func (f *FileStore) StoreConfig() *StoreConfig {
	return f.cfg
}

func (f *FileStore) GetChannel(channelID string, channelType uint8) (*ChannelInfo, error) {

	value, closer, err := f.db.Get([]byte(f.getChannelKey(channelID, channelType)))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
	}
	defer closer.Close()
	var data map[string]interface{}
	err = wkutil.ReadJSONByByte(value, &data)
	if err != nil {
		return nil, err
	}
	channelInfo := &ChannelInfo{}
	channelInfo.ChannelID = channelID
	channelInfo.ChannelType = channelType
	channelInfo.from(data)
	return channelInfo, nil
}

func (f *FileStore) AddOrUpdateChannel(channelInfo *ChannelInfo) error {
	return f.db.Set(f.getChannelKey(channelInfo.ChannelID, channelInfo.ChannelType), []byte(wkutil.ToJSON(channelInfo.ToMap())), f.wo)
}

func (f *FileStore) ExistChannel(channelID string, channelType uint8) (bool, error) {
	value, err := f.GetChannel(channelID, channelType)
	if err != nil {
		return false, err
	}
	if value != nil {
		return true, nil
	}
	return false, nil
}

func (f *FileStore) GetUserToken(uid string, deviceFlag uint8) (string, uint8, error) {
	value, closer, err := f.db.Get([]byte(f.getUserTokenKey(uid, deviceFlag)))
	if err != nil {
		if err == pebble.ErrNotFound {
			return "", 0, errors.New("not found")
		}
		return "", 0, err
	}
	defer closer.Close()

	var resultMap map[string]string
	err = wkutil.ReadJSONByByte(value, &resultMap)
	if err != nil {
		return "", 0, err
	}
	token := resultMap["token"]
	level, _ := strconv.Atoi(resultMap["device_level"])
	return token, uint8(level), nil
}

// UpdateUserToken UpdateUserToken
func (f *FileStore) UpdateUserToken(uid string, deviceFlag uint8, deviceLevel uint8, token string) error {

	return f.db.Set([]byte(f.getUserTokenKey(uid, deviceFlag)), []byte(wkutil.ToJSON(map[string]string{
		"device_level": strconv.Itoa(int(deviceLevel)),
		"token":        token,
	})), f.wo)
}

func (f *FileStore) SyncMessageOfUser(uid string, startMessageSeq, endMessageSeq uint32, limit int) ([]Message, error) {
	return f.FileStoreForMsg.LoadNextRangeMsgs(fmt.Sprintf("%s%s", UserQueuePrefix, uid), wkproto.ChannelTypePerson, startMessageSeq, endMessageSeq, limit)
}

func (f *FileStore) AddSubscribers(channelID string, channelType uint8, uids []string) error {
	key := f.getSubscribersKey(channelID, channelType)
	return f.addList(key, uids)
}

func (f *FileStore) RemoveSubscribers(channelID string, channelType uint8, uids []string) error {
	key := f.getSubscribersKey(channelID, channelType)
	return f.removeList(key, uids)
}

func (f *FileStore) GetSubscribers(channelID string, channelType uint8) ([]string, error) {
	key := f.getSubscribersKey(channelID, channelType)
	return f.getList(key)
}

func (f *FileStore) RemoveAllSubscriber(channelID string, channelType uint8) error {
	key := f.getSubscribersKey(channelID, channelType)
	return f.db.Delete([]byte(key), f.wo)
}

func (f *FileStore) GetAllowlist(channelID string, channelType uint8) ([]string, error) {
	key := f.getAllowlistKey(channelID, channelType)
	return f.getList(key)
}

func (f *FileStore) AddAllowlist(channelID string, channelType uint8, uids []string) error {
	key := f.getAllowlistKey(channelID, channelType)
	return f.addList(key, uids)
}

func (f *FileStore) RemoveAllowlist(channelID string, channelType uint8, uids []string) error {
	key := f.getAllowlistKey(channelID, channelType)
	return f.removeList(key, uids)
}

func (f *FileStore) RemoveAllAllowlist(channelID string, channelType uint8) error {
	key := f.getAllowlistKey(channelID, channelType)
	f.lock.Lock(key)
	defer f.lock.Unlock(key)
	return f.delete([]byte(key))
}

func (f *FileStore) AddDenylist(channelID string, channelType uint8, uids []string) error {
	key := f.getDenylistKey(channelID, channelType)
	return f.addList(key, uids)
}

func (f *FileStore) RemoveDenylist(channelID string, channelType uint8, uids []string) error {
	key := f.getDenylistKey(channelID, channelType)
	return f.removeList(key, uids)
}

func (f *FileStore) RemoveAllDenylist(channelID string, channelType uint8) error {
	key := f.getDenylistKey(channelID, channelType)
	return f.delete([]byte(key))
}

func (f *FileStore) GetDenylist(channelID string, channelType uint8) ([]string, error) {
	key := f.getDenylistKey(channelID, channelType)
	return f.getList(key)
}

func (f *FileStore) DeleteChannel(channelID string, channelType uint8) error {
	err := f.delete([]byte(f.getChannelKey(channelID, channelType)))
	if err != nil {
		return err
	}
	return nil
}

// GetMessageOfUserCursor GetMessageOfUserCursor
func (f *FileStore) GetMessageOfUserCursor(uid string) (uint32, error) {
	var offset uint32 = 0
	value, closer, err := f.db.Get([]byte(f.getMessageOfUserCursorKey(uid)))
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()

	if len(value) > 0 {
		offset64, _ := strconv.ParseUint(string(value), 10, 64)
		offset = uint32(offset64)

	}
	return offset, nil
}

func (f *FileStore) UpdateMessageOfUserCursorIfNeed(uid string, messageSeq uint32) error {

	key := f.getMessageOfUserCursorKey(uid)
	keyBytes := []byte(key)
	f.lock.Lock(key)
	defer f.lock.Unlock(key)

	lastSeq := f.getTopic(fmt.Sprintf("%s%s", UserQueuePrefix, uid), wkproto.ChannelTypePerson).getLastMsgSeq()
	actOffset := messageSeq
	if messageSeq > lastSeq { // 如果传过来的大于系统里最新的 则用最新的
		actOffset = lastSeq
	}

	value, closer, err := f.db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}
	if len(value) > 0 {
		offset64, _ := strconv.ParseUint(string(value), 10, 64)
		oldOffset := uint32(offset64)
		if actOffset <= oldOffset && oldOffset < lastSeq { // 新的
			return nil
		}
	}
	return f.db.Set(keyBytes, []byte(strconv.FormatUint(uint64(actOffset), 10)), f.wo)

}

func (f *FileStore) AddSystemUIDs(uids []string) error {

	f.lock.Lock(f.getSystemUIDsKey())
	defer f.lock.Unlock(f.getSystemUIDsKey())

	keyBytes := []byte(f.getSystemUIDsKey())

	value, closer, err := f.db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}
	list := make([]string, 0)
	if len(value) > 0 {
		values := strings.Split(string(value), ",")
		if len(values) > 0 {
			for _, uid := range uids {
				exist := false
				for _, oldUid := range values {
					if oldUid == uid {
						exist = true
						break
					}
				}
				if !exist {
					list = append(list, uid)
				}
			}
			list = append(list, values...)
		}
	} else {
		list = append(list, uids...)
	}
	return f.db.Set(keyBytes, []byte(strings.Join(list, ",")), f.wo)
}

func (f *FileStore) RemoveSystemUIDs(uids []string) error {
	f.lock.Lock(f.getSystemUIDsKey())
	defer f.lock.Unlock(f.getSystemUIDsKey())

	keyBytes := []byte(f.getSystemUIDsKey())

	value, closer, err := f.db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	list := make([]string, 0)
	if len(value) > 0 {
		values := strings.Split(string(value), ",")
		if len(values) > 0 {
			for _, v := range values {
				var has = false
				for _, uid := range uids {
					if v == uid {
						has = true
						break
					}
				}
				if !has {
					list = append(list, v)
				}
			}
		}
	}
	return f.db.Set(keyBytes, []byte(strings.Join(list, ",")), f.wo)
}

func (f *FileStore) GetSystemUIDs() ([]string, error) {
	uids := make([]string, 0)
	value, closer, err := f.db.Get([]byte(f.getSystemUIDsKey()))
	if err != nil && err != pebble.ErrNotFound {
		if err != pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if closer != nil {
		defer closer.Close()
	}

	if len(value) > 0 {
		uids = strings.Split(string(value), ",")
	}
	return uids, nil
}

func (f *FileStore) AddIPBlacklist(ips []string) error {
	return f.addList(f.getIpBlacklistKey(), ips)
}

func (f *FileStore) RemoveIPBlacklist(ips []string) error {

	return f.removeList(f.getIpBlacklistKey(), ips)
}

func (f *FileStore) GetIPBlacklist() ([]string, error) {

	return f.getList(f.getIpBlacklistKey())
}

func (f *FileStore) AddOrUpdateConversations(uid string, conversations []*Conversation) error {
	if len(conversations) == 0 {
		return nil
	}
	batch := f.db.NewBatch()
	for _, conversation := range conversations {
		enc := wkproto.NewEncoder()
		encodeConversation(conversation, enc)
		err := batch.Set([]byte(f.getConversationKey(uid, conversation.ChannelID, conversation.ChannelType)), enc.Bytes(), f.wo)
		enc.End()
		if err != nil {
			return err
		}
	}
	return batch.Commit(f.wo)
}

func (f *FileStore) GetConversations(uid string) ([]*Conversation, error) {

	lowKey := []byte(f.getConversationLowKey(uid))
	highKey := []byte(f.getConversationHighKey(uid))

	iter := f.db.NewIter(&pebble.IterOptions{
		LowerBound: lowKey,
		UpperBound: highKey,
	})

	defer iter.Close()

	conversations := make([]*Conversation, 0)

	for iter.First(); iter.Valid(); iter.Next() {
		decoder := wkproto.NewDecoder(iter.Value())
		conversation, err := decodeConversation(decoder)
		if err != nil {
			return nil, err
		}
		conversations = append(conversations, conversation)
	}
	return conversations, nil
}

func (f *FileStore) GetConversation(uid string, channelID string, channelType uint8) (*Conversation, error) {
	value, closer, err := f.db.Get([]byte(f.getConversationKey(uid, channelID, channelType)))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()
	decoder := wkproto.NewDecoder(value)
	return decodeConversation(decoder)
}
func (f *FileStore) DeleteConversation(uid string, channelID string, channelType uint8) error {
	return f.db.Delete([]byte(f.getConversationKey(uid, channelID, channelType)), f.wo)
}

func (f *FileStore) AppendMessageOfNotifyQueue(messages []Message) error {

	if len(messages) == 0 {
		return nil
	}

	batch := f.db.NewBatch()
	for _, message := range messages {
		messageID := message.GetMessageID()
		if messageID == 0 {
			return errors.New("messageID is 0")
		}

		err := batch.Set([]byte(f.getNotifyQueueKey(messageID)), message.Encode(), f.wo)
		if err != nil {
			return err
		}
	}
	err := batch.Commit(f.wo)
	if err != nil {
		return err
	}
	return nil
}

func (f *FileStore) RemoveMessagesOfNotifyQueue(messageIDs []int64) error {
	if len(messageIDs) == 0 {
		return nil
	}

	for _, messageID := range messageIDs {
		err := f.db.Delete([]byte(f.getNotifyQueueKey(messageID)), f.wo)
		if err != nil {
			return err
		}
	}
	return nil
}

func (f *FileStore) GetMessagesOfNotifyQueue(count int) ([]Message, error) {
	messages := make([]Message, 0)

	iter := f.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(f.getNotifyQueueKey(0)),
		UpperBound: []byte(f.getNotifyQueueKey(math.MaxInt64)),
	})

	defer iter.Close()

	i := 0
	for iter.First(); iter.Valid(); iter.Next() {
		if i > count-1 {
			break
		}
		message, err := f.cfg.DecodeMessageFnc(iter.Value())
		if err != nil {
			f.Error("decode message fail", zap.Error(err))
			continue
		}
		messages = append(messages, message)
		i++
	}
	return messages, nil
}

func (f *FileStore) AddPeerInFlightData(data []*PeerInFlightDataModel) error {

	if len(data) <= 0 {
		return nil
	}
	return f.db.Set([]byte(f.getPeerInFlightDataKey()), []byte(wkutil.ToJSON(data)), f.wo)
}

func (f *FileStore) ClearPeerInFlightData() error {
	return f.db.Delete([]byte(f.getPeerInFlightDataKey()), f.wo)
}

func (f *FileStore) GetPeerInFlightData() ([]*PeerInFlightDataModel, error) {
	var data []*PeerInFlightDataModel
	value, closer, err := f.db.Get([]byte(f.getPeerInFlightDataKey()))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()

	if len(value) > 0 {
		err = wkutil.ReadJSONByByte(value, &data)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

// 保存频道的最大消息序号
func (f *FileStore) SaveChannelMaxMessageSeq(channelID string, channelType uint8, maxMsgSeq uint32) error {
	maxMsgSeqBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(maxMsgSeqBytes, maxMsgSeq)

	lastTimeBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(lastTimeBytes, uint64(time.Now().UnixNano()))

	return f.db.Set([]byte(f.getChannelMaxMessageSeqKey(channelID, channelType)), append(maxMsgSeqBytes, lastTimeBytes...), f.wo)
}

// 获取频道的最大消息序号 和 最后一次写入的时间
func (f *FileStore) GetChannelMaxMessageSeq(channelID string, channelType uint8) (uint32, uint64, error) {
	value, closer, err := f.db.Get([]byte(f.getChannelMaxMessageSeqKey(channelID, channelType)))
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	defer closer.Close()

	return binary.BigEndian.Uint32(value[:4]), binary.BigEndian.Uint64(value[4:]), nil
}

func (f *FileStore) SaveChannelClusterConfig(channelID string, channelType uint8, data []byte) error {
	return f.db.Set([]byte(f.getChannelClusterConfigKey(channelID, channelType)), data, f.wo)
}

func (f *FileStore) GetChannelClusterConfig(channelID string, channelType uint8) ([]byte, error) {
	value, closer, err := f.db.Get([]byte(f.getChannelClusterConfigKey(channelID, channelType)))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	defer closer.Close()
	return value, nil
}

func (f *FileStore) DeleteChannelClusterConfig(channelID string, channelType uint8) error {
	return f.db.Delete([]byte(f.getChannelClusterConfigKey(channelID, channelType)), f.wo)
}

func (f *FileStore) getChannelMaxMessageSeqKey(channelID string, channelType uint8) string {
	slot := wkutil.GetSlotNum(f.cfg.SlotNum, channelID)
	return fmt.Sprintf("/slots/%s/channelmaxseq/channels/%03d/%s", f.getSlotFillFormat(slot), channelType, channelID)
}

func (f *FileStore) getUserTokenKey(uid string, deviceFlag uint8) string {
	slot := wkutil.GetSlotNum(f.cfg.SlotNum, uid)
	return fmt.Sprintf("/slots/%s/usertoken/users/%s/%03d", f.getSlotFillFormat(slot), uid, deviceFlag)
}
func (f *FileStore) getChannelKey(channelID string, channelType uint8) []byte {
	slotID := wkutil.GetSlotNum(f.cfg.SlotNum, channelID)
	return []byte(fmt.Sprintf("/slots/%s/channelinfo/channels/%03d/%s", f.getSlotFillFormat(slotID), channelType, channelID))
}

func (f *FileStore) getSubscribersKey(channelID string, channelType uint8) string {
	slot := wkutil.GetSlotNum(f.cfg.SlotNum, channelID)
	return fmt.Sprintf("/slots/%s/subscriber/channels/%03d/%s", f.getSlotFillFormat(slot), channelType, channelID)
}

func (f *FileStore) getDenylistKey(channelID string, channelType uint8) string {
	slot := wkutil.GetSlotNum(f.cfg.SlotNum, channelID)
	return fmt.Sprintf("/slots/%s/denylist/channels/%03d/%s", f.getSlotFillFormat(slot), channelType, channelID)
}
func (f *FileStore) getAllowlistKey(channelID string, channelType uint8) string {
	slot := wkutil.GetSlotNum(f.cfg.SlotNum, channelID)
	return fmt.Sprintf("/slots/%s/allowlist/channels/%03d/%s", f.getSlotFillFormat(slot), channelType, channelID)
}

func (f *FileStore) getSystemUIDsKey() string {
	return "/systemuids"
}

func (f *FileStore) getMessageOfUserCursorKey(uid string) string {
	slot := wkutil.GetSlotNum(f.cfg.SlotNum, uid)
	return fmt.Sprintf("/slots/%s/messagecursor/users/%s", f.getSlotFillFormat(slot), uid)
}

func (f *FileStore) getIpBlacklistKey() string {
	return "/ipblacklist"
}

func (f *FileStore) getConversationKey(uid string, channelID string, channelType uint8) string {
	slotID := f.slotNum(uid)
	return fmt.Sprintf("/slots/%s/conversation/users/%s/channels/%03d/%s", f.getSlotFillFormat(slotID), uid, channelType, channelID)
}

func (f *FileStore) getConversationLowKey(uid string) string {
	slotID := f.slotNum(uid)
	return fmt.Sprintf("/slots/%s/conversation/users/%s/channels/%03d", f.getSlotFillFormat(slotID), uid, 0)
}

func (f *FileStore) getConversationHighKey(uid string) string {
	slotID := f.slotNum(uid)
	return fmt.Sprintf("/slots/%s/conversation/users/%s/channels/%03d", f.getSlotFillFormat(slotID), uid, math.MaxUint8)
}

func (f *FileStore) getChannelClusterConfigKey(channelId string, channelType uint8) string {
	slot := wkutil.GetSlotNum(f.cfg.SlotNum, channelId)
	return fmt.Sprintf("/slots/%s/channelclusterconfig/channels/%03d/%s", f.getSlotFillFormat(slot), channelType, channelId)
}

func (f *FileStore) getNotifyQueueKey(messageID int64) string {
	return fmt.Sprintf("/notifyqueue/messages/%d", messageID)
}

func (f *FileStore) getPeerInFlightDataKey() string {
	return "/peerinflightdata"
}

func (f *FileStore) getSlotFillFormat(slotID uint32) string {
	return wkutil.GetSlotFillFormat(int(slotID), f.cfg.SlotNum)
}

func (f *FileStore) getSlotFillFormatMax() string {
	return wkutil.GetSlotFillFormat(f.cfg.SlotNum, f.cfg.SlotNum)
}

func (f *FileStore) getSlotFillFormatMin() string {
	return wkutil.GetSlotFillFormat(0, f.cfg.SlotNum)
}

func (f *FileStore) slotNum(key string) uint32 {
	return wkutil.GetSlotNum(f.cfg.SlotNum, key)
}

func (f *FileStore) delete(key []byte) error {
	return f.db.Delete(key, f.wo)
}
func (f *FileStore) removeList(key string, uids []string) error {
	f.lock.Lock(key)
	defer f.lock.Unlock(key)
	keyBytes := []byte(key)

	value, closer, err := f.db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	if len(value) > 0 {
		values := strings.Split(string(value), ",")
		if len(values) > 0 {
			list := make([]string, 0)
			for _, v := range values {
				var has = false
				for _, uid := range uids {
					if v == uid {
						has = true
						break
					}
				}
				if !has {
					list = append(list, v)
				}
			}
			return f.db.Set(keyBytes, []byte(strings.Join(list, ",")), f.wo)
		}
	}
	return nil

}

func (f *FileStore) getList(key string) ([]string, error) {
	f.lock.Lock(key)
	defer f.lock.Unlock(key)

	keyBytes := []byte(key)

	value, closer, err := f.db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return nil, err
	}
	if closer != nil {
		defer closer.Close()
	}
	list := make([]string, 0)
	if len(value) > 0 {
		values := strings.Split(string(value), ",")
		if len(values) > 0 {
			list = append(list, values...)
		}
	}
	return list, nil
}

func (f *FileStore) addList(key string, valueList []string) error {
	f.lock.Lock(key)
	defer f.lock.Unlock(key)

	keyBytes := []byte(key)

	value, closer, err := f.db.Get(keyBytes)
	if err != nil && err != pebble.ErrNotFound {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}
	list := make([]string, 0)
	if len(value) > 0 {
		values := strings.Split(string(value), ",")
		if len(values) > 0 {
			list = append(list, values...)
		}
	}
	list = append(list, valueList...)
	return f.db.Set(keyBytes, []byte(strings.Join(list, ",")), f.wo)
}
