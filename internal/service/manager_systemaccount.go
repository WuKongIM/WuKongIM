package service

var SystemAccountManager ISystemAccountMgr

type ISystemAccountMgr interface {
	// IsSystemAccount 是否是系统账号
	IsSystemAccount(uid string) bool

	// AddSystemUids 添加系统账号
	AddSystemUids(uids []string) error

	// AddSystemUidsToCache 仅仅添加到缓存内
	AddSystemUidsToCache(uids []string)

	// RemoveSystemUids 移除系统账号
	RemoveSystemUids(uids []string) error
	// RemoveSystemUidsFromCache 仅仅移除缓存中的系统账号
	RemoveSystemUidsFromCache(uids []string)
}
