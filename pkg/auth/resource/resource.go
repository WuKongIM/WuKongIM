package resource

type Id string

// 槽位资源
var Slot = slot{
	Migrate:  "slotMigrate",  // 迁移槽位
	Election: "slotElection", // 槽位选举
}

// 频道资源
var ClusterChannel = channel{
	Migrate: "clusterchannelMigrate", // 迁移频道
	Start:   "clusterchannelStart",   // 启动频道
	Stop:    "clusterchannelStop",    // 停止频道
}

// 插件资源
var Plugin = plugin{
	Uninstall:    "pluginUninstall",    // 卸载插件
	ConfigUpdate: "pluginConfigUpdate", // 更新插件配置
}

// 插件用户资源
var PluginUser = pluginUser{
	Add:    "pluginUserAdd",    // 添加插件用户
	Delete: "pluginUserDelete", // 删除插件用户
}

type slot struct {
	Migrate  Id
	Election Id
}

type channel struct {
	Migrate Id
	Start   Id
	Stop    Id
}

type plugin struct {
	Uninstall    Id
	ConfigUpdate Id
}

type pluginUser struct {
	Add    Id
	Delete Id
}

var All Id = "*"
