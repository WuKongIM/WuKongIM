package resource

type Id string

// 槽位资源
var Slot = slot{
	Migrate: "slotMigrate", // 迁移槽位
}

// 频道资源
var ClusterChannel = channel{
	Migrate: "clusterchannelMigrate", // 迁移频道
	Start:   "clusterchannelStart",   // 启动频道
	Stop:    "clusterchannelStop",    // 停止频道
}

type slot struct {
	Migrate Id
}

type channel struct {
	Migrate Id
	Start   Id
	Stop    Id
}

var All Id = "*"
