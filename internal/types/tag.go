package types

type Tag struct {
	Key   string
	Nodes []*Node
}

type Node struct {
	// 节点id
	LeaderId uint64
	// 节点id对应的用户集合
	Uids []string
	// 用户涉及到的slot
	SlotIds []uint32
}
