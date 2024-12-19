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

func (r Role) String() string {
	switch r {
	case RoleFollower:
		return "RoleFollower"
	case RoleLeader:
		return "RoleLeader"
	default:
		return "RoleUnknown"
	}
}

const LocalNode uint64 = 0
