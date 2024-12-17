package types

import "time"

type Tag struct {
	Key   string
	Nodes []*Node
	// 最后一次获取时间
	LastGetTime time.Time
}

type Node struct {
	// 节点id
	LeaderId uint64
	// 节点id对应的用户集合
	Uids []string
	// 用户涉及到的slot
	SlotIds []uint32
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
