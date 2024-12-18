package service

import "github.com/WuKongIM/WuKongIM/internal/types"

var TagManager ITagManager

type ITagManager interface {
	// AddUsers 向指定tag里增加用户
	AddUsers(tagKey string, uids []string) error
	RemoveUsers(tagKey string, uids []string) error
	// RemoveTag 移除tag
	RemoveTag(tagKey string)
	// RenameTag 重命名tag
	RenameTag(oldTagKey, newTagKey string) error
	// MakeTag 创建tag
	MakeTag(uids []string) (*types.Tag, error)
	MakeTagWithTagKey(tagKey string, uids []string) (*types.Tag, error)
	//	 GetUsers 获取tag下的所有用户
	GetUsers(tagKey string) []string
	// GetTag 获取tag
	Get(tagKey string) *types.Tag
	// Exist 是否存在tag
	Exist(tagKey string) bool
	// SetChannelTag 设置频道对应的tag
	SetChannelTag(fakeChannelId string, channelType uint8, tagKey string)
	// GetChannelTag 获取频道对应的tag
	GetChannelTag(fakeChannelId string, channelType uint8) string
}
