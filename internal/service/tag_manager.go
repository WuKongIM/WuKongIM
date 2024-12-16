package service

import "github.com/WuKongIM/WuKongIM/internal/types"

var TagMananger TagMgr

type TagMgr interface {
	// AddUsers 向指定tag里增加用户
	AddUsers(tagKey string, uids []string)
	// RemoveTag 移除tag
	RemoveTag(tagKey string)
	//	 GetUsers 获取tag下的所有用户
	GetUsers(tagKey string) []string
	// GetUsersByNodeId 获取tag下的指定节点的用户
	GetUsersByNodeId(tagKey string, nodeId uint64) []string
	// GetTag 获取tag
	Get(tagKey string) *types.Tag
	// Exist 是否存在tag
	Exist(tagKey string) bool
}
