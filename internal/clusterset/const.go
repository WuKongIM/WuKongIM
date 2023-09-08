package clusterset

import "fmt"

type NodeRole string

const (
	NodeRolePeer    NodeRole = "peer"
	NodeRoleGateway NodeRole = "gateway"
)

func (n NodeRole) String() string {
	return string(n)
}

const (
	StatusClustersetconfigIsLatest = 201 // 集群配置已经是最新的
)

type bootstrapStatus int

const (
	bootstrapStatusInit bootstrapStatus = iota
	bootstrapStatusWaitConnect
	bootstrapStatusWaitJoin
	bootstrapStatusReady
)

func GetFakeNodeID(nodeID uint64) string {
	return fmt.Sprintf("%d@%s", nodeID, NodeRolePeer.String())
}
