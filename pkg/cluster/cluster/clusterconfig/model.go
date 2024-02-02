package clusterconfig

type NodeInfo struct {
	Id            uint64
	ApiServerAddr string
}

var EmptyNodeInfo = NodeInfo{}

type SlotInfo struct {
	Id uint64
}
