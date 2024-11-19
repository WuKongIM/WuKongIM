package server

import (
	"sync"
	"time"

	"github.com/RussellLuo/timingwheel"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type tagManager struct {
	receiverPrefix string

	tags []*tag

	mu         sync.RWMutex
	s          *Server
	cleanTimer *timingwheel.Timer
}

func newTagManager(s *Server) *tagManager {
	return &tagManager{
		receiverPrefix: "receiver:",
		s:              s,
	}
}

func (t *tagManager) start() error {
	t.cleanTimer = t.s.Schedule(time.Minute, func() {
		t.mu.Lock()
		defer t.mu.Unlock()

		tagLen := len(t.tags)

		var needRemoveTags []*tag
		for i := 0; i < tagLen; i++ {
			tg := t.tags[i]
			// 超过指定时间没有活跃，移除tag
			if time.Since(tg.activeAt) > time.Minute*5 {
				t.s.Info("tag is expired, remove it", zap.String("key", tg.key))
				for _, tag := range t.tags {
					if tag.key == tg.key {
						needRemoveTags = append(needRemoveTags, tag)
						break
					}
				}
			}
		}
		if len(needRemoveTags) > 0 {
			newTags := make([]*tag, 0, len(t.tags)-len(needRemoveTags))
			for _, tag := range t.tags {
				var exist bool
				for _, needRemoveTag := range needRemoveTags {
					if tag.key == needRemoveTag.key {
						exist = true
						break
					}
				}
				if !exist {
					newTags = append(newTags, tag)
				}
			}
			t.tags = newTags
		}
	})
	return nil
}

func (t *tagManager) stop() {
	if t.cleanTimer != nil {
		t.cleanTimer.Stop()
	}
}

// 添加频道接受者tag
func (t *tagManager) addOrUpdateReceiverTag(key string, users []*nodeUsers, channelId string, channelType uint8) *tag {
	t.mu.Lock()
	defer t.mu.Unlock()

	var existTag *tag
	for _, tag := range t.tags {
		if tag.key == key {
			tag.users = users
			existTag = tag
			break
		}

	}
	if existTag == nil {
		nw := time.Now()
		existTag = &tag{
			key:         key,
			users:       users,
			channelId:   channelId,
			channelType: channelType,
			activeAt:    nw,
			createdAt:   nw,
		}
		t.tags = append(t.tags, existTag)
	}
	return existTag
}

// 通过key获取频道接受者tag
func (t *tagManager) getReceiverTag(key string) *tag {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, tag := range t.tags {
		if tag.key == key {
			tag.activeAt = time.Now()
			return tag
		}
	}
	return nil
}

// 在指定节点内是否存在指定的用户
func (t *tagManager) existUserInNode(key string, uid string, nodeId uint64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var existTag *tag
	for _, tag := range t.tags {
		if tag.key == key {
			existTag = tag
			break
		}
	}
	if existTag == nil {
		return false
	}

	for _, nodeUser := range existTag.users {
		if nodeUser.nodeId == nodeId {
			for _, u := range nodeUser.uids {
				if u == uid {
					return true
				}
			}
			return false
		}
	}
	return false
}

// // 获取指定频道最新的tag的用户列表
// func (t *tagManager) getReceiverLastTagWithChannel(channelId string, channelType uint8) *tag {
// 	t.mu.RLock()
// 	defer t.mu.RUnlock()
// 	var lastTag *tag
// 	for _, tag := range t.tags {
// 		if tag.channelId == channelId && tag.channelType == channelType {
// 			if lastTag == nil {
// 				lastTag = tag
// 			} else if tag.activeAt.After(lastTag.activeAt) {
// 				lastTag = tag
// 			}
// 		}
// 	}
// 	return lastTag
// }

// 立马释放频道接受者tag
func (t *tagManager) releaseReceiverTagNow(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, tag := range t.tags {
		if tag.key == key {
			t.tags = append(t.tags[:i], t.tags[i+1:]...)
			return
		}
	}
}

// 释放频道接受者tag
// func (t *tagManager) releaseReceiverTag(key string) {
// 	t.mu.Lock()
// 	defer t.mu.Unlock()
// 	for _, tag := range t.tags {
// 		if tag.key == key {
// 			tag.ref.Dec()
// 			return
// 		}
// 	}
// }

// func (t *tagManager) receiverTagKey(channelId string, channelType uint8) string {
// 	return fmt.Sprintf("%s%d%s", t.receiverPrefix, channelType, channelId)
// }

type tagReq struct {
	channelId   string
	channelType uint8
	tagKey      string
	nodeId      uint64
}

func (t *tagReq) Marshal() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(t.channelId)
	enc.WriteUint8(t.channelType)
	enc.WriteString(t.tagKey)
	enc.WriteUint64(t.nodeId)
	return enc.Bytes()
}

func (t *tagReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if t.channelId, err = dec.String(); err != nil {
		return err
	}
	if t.channelType, err = dec.Uint8(); err != nil {
		return err
	}
	if t.tagKey, err = dec.String(); err != nil {
		return err
	}
	if t.nodeId, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

type tagResp struct {
	tagKey string
	uids   []string
}

func (t *tagResp) Marshal() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteString(t.tagKey)

	enc.WriteUint32(uint32(len(t.uids)))
	for _, uid := range t.uids {
		enc.WriteString(uid)
	}

	return enc.Bytes()
}

func (t *tagResp) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)

	tagKey, err := dec.String()
	if err != nil {
		return err
	}
	t.tagKey = tagKey

	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	t.uids = make([]string, 0, count)
	for i := 0; i < int(count); i++ {
		var uid string
		if uid, err = dec.String(); err != nil {
			return err
		}
		t.uids = append(t.uids, uid)
	}
	return nil
}

type tag struct {
	key         string
	channelId   string
	channelType uint8
	users       []*nodeUsers
	createdAt   time.Time // 创建时间
	activeAt    time.Time // 最后一次激活时间
}

// 获取指定节点的用户列表
func (t *tag) getNodeUsers(nodeId uint64) *nodeUsers {
	for _, u := range t.users {
		if u.nodeId == nodeId {
			return u
		}
	}
	return nil
}

func (t *tag) Marshal() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(t.key)
	enc.WriteUint32(uint32(len(t.users)))
	for _, u := range t.users {
		enc.WriteUint64(u.nodeId)
		enc.WriteUint32(uint32(len(u.uids)))
		for _, uid := range u.uids {
			enc.WriteString(uid)
		}
	}
	return enc.Bytes()
}

func (t *tag) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if t.key, err = dec.String(); err != nil {
		return err
	}
	var usersLen uint32
	if usersLen, err = dec.Uint32(); err != nil {
		return err
	}
	t.users = make([]*nodeUsers, 0, usersLen)
	for i := uint32(0); i < usersLen; i++ {
		var nodeUsers nodeUsers
		if nodeUsers.nodeId, err = dec.Uint64(); err != nil {
			return err
		}
		var uidsLen uint32
		if uidsLen, err = dec.Uint32(); err != nil {
			return err
		}
		nodeUsers.uids = make([]string, 0, uidsLen)
		for j := uint32(0); j < uidsLen; j++ {
			var uid string
			if uid, err = dec.String(); err != nil {
				return err
			}
			nodeUsers.uids = append(nodeUsers.uids, uid)
		}
		t.users = append(t.users, &nodeUsers)
	}
	return nil

}

// 用户列表和所属的节点
type nodeUsers struct {
	nodeId uint64

	uids []string
}
