package types

import (
	"fmt"
	"time"
)

type Tag struct {
	Key   string
	Nodes []*Node
	// 最后一次获取时间
	LastGetTime time.Time
	NodeVersion uint64 // 生成tag时的当前节点版本号，如果当前节点版本号大于生成tag时的节点版本号，则tag失效
}

func (t *Tag) String() string {

	return fmt.Sprintf("Tag{Key:%s, Nodes:%v, LastGetTime:%v}", t.Key, t.Nodes, t.LastGetTime)
}

type Node struct {
	// 节点id
	LeaderId uint64
	// 节点id对应的用户集合
	Uids []string
	// 用户涉及到的slot
	SlotIds []uint32
}

func (n *Node) String() string {
	return fmt.Sprintf("Node{LeaderId:%d, Uids:%v, SlotIds:%v}", n.LeaderId, n.Uids, n.SlotIds)
}

func (t *Tag) GetNodeUsers(nodeId uint64) []string {
	for _, node := range t.Nodes {
		if node.LeaderId == nodeId {
			return node.Uids
		}
	}
	return nil
}

func (t *Tag) ExistUserInNode(uid string, nodeId uint64) bool {
	for _, node := range t.Nodes {
		if node.LeaderId == nodeId {
			for _, u := range node.Uids {
				if u == uid {
					return true
				}
			}
		}
	}
	return false
}

func (t *Tag) GetUsers() []string {
	userCount := 0
	for _, node := range t.Nodes {
		userCount += len(node.Uids)
	}
	users := make([]string, 0, userCount)
	for _, node := range t.Nodes {
		users = append(users, node.Uids...)
	}
	return users
}
