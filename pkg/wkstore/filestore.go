package wkstore

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/keylock"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	bolt "go.etcd.io/bbolt"
	"go.uber.org/zap"
)

const UserQueuePrefix = "userqueue_"

type FileStore struct {
	cfg                       *StoreConfig
	db                        *bolt.DB
	rootBucketPrefix          string
	messageOfUserCursorPrefix string

	lock *keylock.KeyLock

	userTokenPrefix        string
	channelPrefix          string
	subscribersPrefix      string
	denylistPrefix         string
	allowlistPrefix        string
	notifyQueuePrefix      string
	userSeqPrefix          string
	nodeInFlightDataPrefix string
	systemUIDsKey          string

	*FileStoreForMsg
}

func NewFileStore(cfg *StoreConfig) *FileStore {

	f := &FileStore{
		cfg:                       cfg,
		lock:                      keylock.NewKeyLock(),
		rootBucketPrefix:          "wukongimRoot",
		messageOfUserCursorPrefix: "messageOfUserCursor:",
		userTokenPrefix:           "userToken:",
		channelPrefix:             "channel:",
		subscribersPrefix:         "subscribers:",
		denylistPrefix:            "denylist:",
		allowlistPrefix:           "allowlist:",
		notifyQueuePrefix:         "notifyQueue",
		userSeqPrefix:             "userSeq:",
		nodeInFlightDataPrefix:    "nodeInFlightData",
		systemUIDsKey:             "systemUIDs",
		FileStoreForMsg:           NewFileStoreForMsg(cfg),
	}

	return f
}

func (f *FileStore) Open() error {
	f.lock.StartCleanLoop()
	var err error
	f.db, err = bolt.Open(filepath.Join(f.cfg.DataDir, "wukongim.db"), 0755, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return err
	}

	err = f.db.Batch(func(t *bolt.Tx) error {
		_, err = t.CreateBucketIfNotExists([]byte(f.rootBucketPrefix))
		if err != nil {
			return err
		}
		_, err = t.CreateBucketIfNotExists([]byte(f.notifyQueuePrefix))
		if err != nil {
			return err
		}
		for i := 0; i < f.cfg.SlotNum; i++ {
			_, err := t.CreateBucketIfNotExists([]byte(fmt.Sprintf("%d", i)))
			if err != nil {
				return err
			}
		}
		return nil
	})
	return err

}

func (f *FileStore) GetChannel(channelID string, channelType uint8) (*ChannelInfo, error) {
	slotNum := f.slotNumForChannel(channelID, channelType)
	value, err := f.get(slotNum, []byte(f.getChannelKey(channelID, channelType)))
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
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

func (f *FileStore) GetUserToken(uid string, deviceFlag uint8) (string, uint8, error) {
	slotNum := f.slotNum(uid)
	value, err := f.get(slotNum, []byte(f.getUserTokenKey(uid, deviceFlag)))
	if err != nil {
		return "", 0, err
	}
	if len(value) == 0 {
		return "", 0, nil
	}
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
	slotNum := f.slotNum(uid)
	return f.set(slotNum, []byte(f.getUserTokenKey(uid, deviceFlag)), []byte(wkutil.ToJSON(map[string]string{
		"device_level": fmt.Sprintf("%d", deviceLevel),
		"token":        token,
	})))
}

func (f *FileStore) SyncMessageOfUser(uid string, startMessageSeq uint32, limit int) ([]Message, error) {

	fmt.Println("SyncMessageOfUser-startMessageSeq--->", uid, startMessageSeq, limit)
	return f.FileStoreForMsg.LoadNextRangeMsgs(fmt.Sprintf("%s%s", UserQueuePrefix, uid), wkproto.ChannelTypePerson, startMessageSeq, 0, limit)
}

func (f *FileStore) AddOrUpdateChannel(channelInfo *ChannelInfo) error {
	slotNum := f.slotNumForChannel(channelInfo.ChannelID, channelInfo.ChannelType)
	return f.set(slotNum, []byte(f.getChannelKey(channelInfo.ChannelID, channelInfo.ChannelType)), []byte(wkutil.ToJSON(channelInfo.ToMap())))
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

func (f *FileStore) AddSubscribers(channelID string, channelType uint8, uids []string) error {
	key := f.getSubscribersKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.addList(slotNum, key, uids)
}

func (f *FileStore) RemoveSubscribers(channelID string, channelType uint8, uids []string) error {
	key := f.getSubscribersKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.removeList(slotNum, key, uids)
}

func (f *FileStore) GetSubscribers(channelID string, channelType uint8) ([]string, error) {
	key := f.getSubscribersKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.getList(slotNum, key)
}

func (f *FileStore) RemoveAllSubscriber(channelID string, channelType uint8) error {
	key := f.getSubscribersKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	err := f.db.Update(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucket(slotNum, t)
		if err != nil {
			return err
		}
		return bucket.Delete([]byte(key))
	})
	return err
}

func (f *FileStore) GetAllowlist(channelID string, channelType uint8) ([]string, error) {
	key := f.getAllowlistKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.getList(slotNum, key)
}

func (f *FileStore) AddAllowlist(channelID string, channelType uint8, uids []string) error {
	key := f.getAllowlistKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.addList(slotNum, key, uids)
}

func (f *FileStore) RemoveAllowlist(channelID string, channelType uint8, uids []string) error {
	key := f.getAllowlistKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.removeList(slotNum, key, uids)
}

func (f *FileStore) RemoveAllAllowlist(channelID string, channelType uint8) error {
	key := f.getAllowlistKey(channelID, channelType)
	f.lock.Lock(key)
	defer f.lock.Unlock(key)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.delete(slotNum, []byte(key))
}

func (f *FileStore) AddDenylist(channelID string, channelType uint8, uids []string) error {
	key := f.getDenylistKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.addList(slotNum, key, uids)
}

func (f *FileStore) RemoveDenylist(channelID string, channelType uint8, uids []string) error {
	key := f.getDenylistKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.removeList(slotNum, key, uids)
}

func (f *FileStore) RemoveAllDenylist(channelID string, channelType uint8) error {
	key := f.getDenylistKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.delete(slotNum, []byte(key))
}

func (f *FileStore) GetDenylist(channelID string, channelType uint8) ([]string, error) {
	key := f.getDenylistKey(channelID, channelType)
	slotNum := f.slotNumForChannel(channelID, channelType)
	return f.getList(slotNum, key)
}

func (f *FileStore) DeleteChannel(channelID string, channelType uint8) error {
	slotNum := f.slotNumForChannel(channelID, channelType)
	err := f.delete(slotNum, []byte(f.getChannelKey(channelID, channelType)))
	if err != nil {
		return err
	}
	return nil
}

// GetMessageOfUserCursor GetMessageOfUserCursor
func (f *FileStore) GetMessageOfUserCursor(uid string) (uint32, error) {
	slot := f.slotNum(uid)
	var offset uint32 = 0
	err := f.db.View(func(t *bolt.Tx) error {
		b, err := f.getSlotBucket(slot, t)
		if err != nil {
			return err
		}
		value := b.Get([]byte(f.getMessageOfUserCursorKey(uid)))
		if len(value) > 0 {
			offset64, _ := strconv.ParseUint(string(value), 10, 64)
			offset = uint32(offset64)
		}
		return nil
	})
	return offset, err
}

func (f *FileStore) UpdateMessageOfUserCursorIfNeed(uid string, messageSeq uint32) error {
	slot := f.slotNum(uid)
	lastSeq := f.getTopic(fmt.Sprintf("%s%s", UserQueuePrefix, uid), wkproto.ChannelTypePerson).getLastMsgSeq()
	actOffset := messageSeq
	if messageSeq > lastSeq { // 如果传过来的大于系统里最新的 则用最新的
		actOffset = lastSeq
	}
	return f.db.Update(func(t *bolt.Tx) error {
		b, err := f.getSlotBucket(slot, t)
		if err != nil {
			return err
		}
		key := f.getMessageOfUserCursorKey(uid)
		value := b.Get([]byte(key))
		if len(value) > 0 {
			offset64, _ := strconv.ParseUint(string(value), 10, 64)
			oldOffset := uint32(offset64)
			if actOffset <= oldOffset && oldOffset < lastSeq { // 新的
				return nil
			}
		}
		return b.Put([]byte(key), []byte(fmt.Sprintf("%d", actOffset)))
	})
}

func (f *FileStore) AddSystemUIDs(uids []string) error {
	err := f.db.Update(func(t *bolt.Tx) error {
		bucket := f.getRootBucket(t)
		value := bucket.Get([]byte(f.systemUIDsKey))
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
		}
		return bucket.Put([]byte(f.systemUIDsKey), []byte(strings.Join(list, ",")))
	})
	return err
}

func (f *FileStore) RemoveSystemUIDs(uids []string) error {
	err := f.db.Update(func(t *bolt.Tx) error {
		bucket := f.getRootBucket(t)
		value := bucket.Get([]byte(f.systemUIDsKey))
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
		return bucket.Put([]byte(f.systemUIDsKey), []byte(strings.Join(list, ",")))
	})
	return err
}

func (f *FileStore) GetSystemUIDs() ([]string, error) {
	uids := make([]string, 0)
	err := f.db.View(func(tx *bolt.Tx) error {
		bucket := f.getRootBucket(tx)
		value := bucket.Get([]byte(f.systemUIDsKey))
		if len(value) > 0 {
			uids = strings.Split(string(value), ",")
		}
		return nil
	})
	return uids, err
}

func (f *FileStore) AddOrUpdateConversations(uid string, conversations []*Conversation) error {
	newConversations, err := f.getNewConversations(uid, conversations)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("conversation:%s", uid)
	return f.db.Update(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucketWithKey(uid, t)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(key), []byte(wkutil.ToJSON(newConversations)))
	})
}

func (f *FileStore) GetConversations(uid string) ([]*Conversation, error) {
	key := fmt.Sprintf("conversation:%s", uid)
	var conversations []*Conversation
	err := f.db.View(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucketWithKey(uid, t)
		if err != nil {
			return err
		}
		value := bucket.Get([]byte(key))
		if len(value) > 0 {
			err = wkutil.ReadJSONByByte(value, &conversations)
			return err
		}
		return nil

	})
	return conversations, err
}

func (f *FileStore) GetConversation(uid string, channelID string, channelType uint8) (*Conversation, error) {
	conversations, err := f.GetConversations(uid)
	if err != nil {
		return nil, err
	}
	if len(conversations) > 0 {
		for _, conversation := range conversations {
			if conversation.ChannelID == channelID && conversation.ChannelType == channelType {
				return conversation, nil
			}
		}
	}
	return nil, nil
}
func (f *FileStore) DeleteConversation(uid string, channelID string, channelType uint8) error {
	conversations, err := f.GetConversations(uid)
	if err != nil {
		return err
	}
	newConversations := make([]*Conversation, 0, len(conversations))
	if len(conversations) > 0 {
		for _, conversation := range conversations {
			if !(conversation.ChannelID == channelID && conversation.ChannelType == channelType) {
				newConversations = append(newConversations, conversation)
			}
		}
	}

	key := fmt.Sprintf("conversation:%s", uid)
	return f.db.Update(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucketWithKey(uid, t)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(key), []byte(wkutil.ToJSON(newConversations)))
	})
}

func (f *FileStore) AppendMessageOfNotifyQueue(messages []Message) error {
	return f.db.Update(func(t *bolt.Tx) error {
		bucket := t.Bucket([]byte(f.notifyQueuePrefix))
		for _, message := range messages {
			seq, err := bucket.NextSequence()
			if err != nil {
				return err
			}
			msgBytes := message.Encode()
			err = bucket.Put(itob(seq), msgBytes)
			if err != nil {
				f.Error("AppendMessageOfNotifyQueue", zap.Error(err))
				continue
			}
		}
		return nil
	})
}

func (f *FileStore) RemoveMessagesOfNotifyQueue(messageIDs []int64) error {
	if len(messageIDs) == 0 {
		return nil
	}
	return f.db.Update(func(t *bolt.Tx) error {
		bucket := t.Bucket([]byte(f.notifyQueuePrefix))
		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			message, err := f.cfg.DecodeMessageFnc(v)
			if err != nil {
				return err
			}
			for _, messageID := range messageIDs {
				if message.GetMessageID() == messageID {
					err = bucket.Delete(k)
					if err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func (f *FileStore) GetMessagesOfNotifyQueue(count int) ([]Message, error) {
	messages := make([]Message, 0)
	err := f.db.View(func(t *bolt.Tx) error {
		bucket := t.Bucket([]byte(f.notifyQueuePrefix))
		c := bucket.Cursor()
		i := 0
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if i > count-1 {
				break
			}
			message, err := f.cfg.DecodeMessageFnc(v)
			if err != nil {
				f.Error("decode message fail", zap.Error(err))
				continue
			}
			messages = append(messages, message)
			i++
		}
		return nil
	})
	return messages, err
}

func (f *FileStore) Close() error {
	f.lock.StopCleanLoop()
	f.FileStoreForMsg.Close()
	f.db.Close()
	return nil

}

func (f *FileStore) get(slot uint32, key []byte) ([]byte, error) {
	var value []byte
	err := f.db.View(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucket(slot, t)
		if err != nil {
			return err
		}
		value = bucket.Get(key)
		return nil
	})
	return value, err
}

func (f *FileStore) getNewConversations(uid string, updateConversations []*Conversation) ([]*Conversation, error) {
	oldConversations, err := f.GetConversations(uid)
	if err != nil {
		return nil, err
	}

	newConversations := make([]*Conversation, 0, len(oldConversations)+len(updateConversations))
	newConversations = append(newConversations, oldConversations...)
	for _, updateConversation := range updateConversations {
		var existConversation *Conversation
		var existIndex = 0
		for idx, oldConversation := range oldConversations {
			if updateConversation.ChannelID == oldConversation.ChannelID && updateConversation.ChannelType == oldConversation.ChannelType {
				existConversation = updateConversation
				existIndex = idx
				break
			}
		}
		if existConversation == nil {
			newConversations = append(newConversations, updateConversation)
		} else {
			newConversations[existIndex] = existConversation
		}
	}
	return newConversations, nil
}

func (f *FileStore) getRootBucket(t *bolt.Tx) *bolt.Bucket {
	return t.Bucket([]byte(f.rootBucketPrefix))
}

func (f *FileStore) getSlotBucketWithKey(key string, t *bolt.Tx) (*bolt.Bucket, error) {
	slot := wkutil.GetSlotNum(f.cfg.SlotNum, key)
	return f.getSlotBucket(slot, t)
}
func (f *FileStore) getSlotBucket(slotNum uint32, t *bolt.Tx) (*bolt.Bucket, error) {
	return t.Bucket([]byte(fmt.Sprintf("%d", slotNum))), nil
}

func (f *FileStore) getUserTokenKey(uid string, deviceFlag uint8) string {
	return fmt.Sprintf("%s%s-%d", f.userTokenPrefix, uid, deviceFlag)
}
func (f *FileStore) getChannelKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%s%s-%d", f.channelPrefix, channelID, channelType)
}

func (f *FileStore) getSubscribersKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%s%s-%d", f.subscribersPrefix, channelID, channelType)
}

func (f *FileStore) getDenylistKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%s%s-%d", f.denylistPrefix, channelID, channelType)
}
func (f *FileStore) getAllowlistKey(channelID string, channelType uint8) string {
	return fmt.Sprintf("%s%s-%d", f.allowlistPrefix, channelID, channelType)
}

func (f *FileStore) getMessageOfUserCursorKey(uid string) string {
	return fmt.Sprintf("%s%s", f.messageOfUserCursorPrefix, uid)
}

func (f *FileStore) slotNum(key string) uint32 {
	return wkutil.GetSlotNum(f.cfg.SlotNum, key)
}

func (f *FileStore) delete(slot uint32, key []byte) error {
	return f.db.Update(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucket(slot, t)
		if err != nil {
			return err
		}
		return bucket.Delete(key)
	})
}

func (f *FileStore) slotNumForChannel(channelID string, channelType uint8) uint32 {
	return wkutil.GetSlotNum(f.cfg.SlotNum, channelID)
}

func (f *FileStore) removeList(slotNum uint32, key string, uids []string) error {
	f.lock.Lock(key)
	defer f.lock.Unlock(key)
	err := f.db.Update(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucket(slotNum, t)
		if err != nil {
			return err
		}
		value := bucket.Get([]byte(key))
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
		return bucket.Put([]byte(key), []byte(strings.Join(list, ",")))
	})
	return err

}

func (f *FileStore) getList(slotNum uint32, key string) ([]string, error) {
	f.lock.Lock(key)
	defer f.lock.Unlock(key)

	var values []string
	err := f.db.View(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucket(slotNum, t)
		if err != nil {
			return err
		}
		value := bucket.Get([]byte(key))
		if len(value) > 0 {
			values = strings.Split(string(value), ",")
			return nil
		}
		return nil
	})
	return values, err
}

func (f *FileStore) addList(slotNum uint32, key string, valueList []string) error {
	f.lock.Lock(key)
	defer f.lock.Unlock(key)

	err := f.db.Update(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucket(slotNum, t)
		if err != nil {
			return err
		}
		value := bucket.Get([]byte(key))
		list := make([]string, 0)
		if len(value) > 0 {
			values := strings.Split(string(value), ",")
			if len(values) > 0 {
				list = append(list, values...)
			}
		}
		list = append(list, valueList...)
		return bucket.Put([]byte(key), []byte(strings.Join(list, ",")))
	})
	return err
}

func (f *FileStore) set(slot uint32, key []byte, value []byte) error {
	err := f.db.Update(func(t *bolt.Tx) error {
		bucket, err := f.getSlotBucket(slot, t)
		if err != nil {
			return err
		}
		return bucket.Put(key, value)
	})
	return err
}
func itob(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}
