package service

import (
	"github.com/WuKongIM/WuKongIM/internal/types"
)

var PluginManager IPluginManager

type IPluginManager interface {
	// Plugins 获取包含指定方法的插件
	Plugins(methods ...types.PluginMethod) []types.Plugin
	// Plugin 获取指定编号的插件
	Plugin(no string) types.Plugin
	// UserIsAI 判断用户是否是AI
	UserIsAI(uid string) bool
	// GetUserPluginNo 获取用户插件编号
	GetUserPluginNo(uid string) (string, error)
}
