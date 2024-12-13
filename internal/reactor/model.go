package reactor

type Role uint8 // 角色

const (
	// 未知
	RoleUnknown Role = iota
	// 副本
	RoleFollower
	// 领导者
	RoleLeader
)

const LocalNode uint64 = 0
