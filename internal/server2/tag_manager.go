package server

import (
	"fmt"
	"sync"
	"time"
)

type tagManager struct {
	receiverPrefix string

	tags []*tag

	mu sync.RWMutex
}

func newTagManager() *tagManager {
	return &tagManager{
		receiverPrefix: "receiver:",
	}
}

func (t *tagManager) start() error {

	return nil
}

func (t *tagManager) stop() {

}

// 添加频道接受者tag
func (t *tagManager) addOrUpdateReceiverTag(channelId string, channelType uint8, users []*nodeUsers) *tag {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := t.receiverTagKey(channelId, channelType)

	var existTag *tag
	for _, tag := range t.tags {
		if tag.key == key {
			tag.users = users
			existTag = tag
			break
		}

	}
	if existTag == nil {
		existTag = &tag{
			key:       key,
			users:     users,
			createdAt: time.Now(),
		}
		t.tags = append(t.tags, existTag)
	}
	return existTag
}

func (t *tagManager) getReceiverTag(channelId string, channelType uint8) *tag {
	t.mu.RLock()
	defer t.mu.RUnlock()
	key := t.receiverTagKey(channelId, channelType)
	for _, tag := range t.tags {
		if tag.key == key {
			return tag
		}
	}
	return nil
}

// 获取频道接受者tag 并引用计数
func (t *tagManager) getReceiverTagAndRef(channelId string, channelType uint8) *tag {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := t.receiverTagKey(channelId, channelType)
	for _, tag := range t.tags {
		if tag.key == key {
			tag.ref++
			return tag
		}
	}
	return nil
}

// 释放频道接受者tag
func (t *tagManager) releaseReceiverTag(channelId string, channelType uint8) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := t.receiverTagKey(channelId, channelType)
	for _, tag := range t.tags {
		if tag.key == key {
			tag.ref--
			return
		}
	}
}

func (t *tagManager) receiverTagKey(channelId string, channelType uint8) string {
	return fmt.Sprintf("%s%d%s", t.receiverPrefix, channelType, channelId)
}

type tag struct {
	key       string
	users     []*nodeUsers
	ref       int       // 引用计数
	createdAt time.Time // 创建时间
}

// 用户列表和所属的节点
type nodeUsers struct {
	nodeId uint64
	uids   []string
}
