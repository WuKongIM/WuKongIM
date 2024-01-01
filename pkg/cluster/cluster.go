package cluster

type ICluster interface {
	Start() error
	Stop()

	// IsUserNode 是否是用户所在的节点
	IsUserNode(uid string) (bool, error)

	// ProposeToSlot 提交数据到指定的slot
	ProposeToSlot(slotID uint32, data []byte) error
	// 追加日志
	AppendLog(slotID uint32, logIndex uint64, data []byte) error
}
