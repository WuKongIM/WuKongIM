package event

import (
	"strings"

	"go.uber.org/zap"
)

func (h *handler) handleCompare() {
	if h.cfgServer.LeaderId() == 0 {
		return
	}
	// 如果配置里自己节点的apiServerAddr配置不存在或不同，则提案配置
	if strings.TrimSpace(h.cfgOptions.ApiServerAddr) != "" {
		localNode := h.cfgServer.Node(h.cfgOptions.NodeId)
		if localNode != nil && localNode.ApiServerAddr != h.cfgOptions.ApiServerAddr {
			err := h.cfgServer.ProposeApiServerAddr(h.cfgOptions.NodeId, h.cfgOptions.ApiServerAddr)
			if err != nil {
				h.Error("ProposeApiServerAddr failed", zap.Error(err))
				return
			}
		}
	}
}
