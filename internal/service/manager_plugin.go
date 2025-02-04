package service

import (
	"github.com/WuKongIM/WuKongIM/internal/types"
)

var PluginManager IPluginManager

type IPluginManager interface {
	Plugins(methods ...types.PluginMethod) []types.Plugin
}
