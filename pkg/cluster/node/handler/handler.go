package handler

import "github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"

type Handler struct {
}

func New() *Handler {
	return &Handler{}
}

func (h *Handler) OnEvent(event *types.Event) {
	return
}
