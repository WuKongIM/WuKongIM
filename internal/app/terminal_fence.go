package app

import (
	"strings"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	"github.com/WuKongIM/WuKongIM/internal/usecase/benchterminal"
)

const (
	benchTerminalMaxSessions  = 2500
	benchTerminalDrainTimeout = 90 * time.Second
)

// wireBenchTerminal composes the one-way terminal cut only when every real
// product drain is available. The API therefore cannot advertise a partial
// proof when gateway, channel append, or Online Delivery is absent.
func (a *App) wireBenchTerminal() {
	if a == nil || a.benchTerminal != nil || a.handler == nil || !a.cfg.Bench.APIEnabled || strings.TrimSpace(a.cfg.Bench.APIToken) == "" || a.channelAppends == nil || a.onlineDelivery == nil {
		return
	}
	gatewayDrainer, ok := a.gateway.(benchterminal.GatewayDrainer)
	if !ok || gatewayDrainer == nil {
		return
	}
	controller := benchterminal.New(benchterminal.Options{
		Gateway:       gatewayDrainer,
		ChannelAppend: a.channelAppends,
		Delivery:      a.onlineDelivery,
		MaxSessions:   benchTerminalMaxSessions,
		DrainTimeout:  benchTerminalDrainTimeout,
		Goroutines:    a.goroutines,
	})
	// The concrete gateway must exist before its drain port can be composed, so
	// construction binds the resulting one-shot controller to the already-built
	// handler before any listener starts. The API receives this same pointer.
	if !a.handler.BindBenchTerminalFence(controller) {
		return
	}
	a.benchTerminal = controller
}

func (a *App) benchTerminalController() accessapi.TerminalFenceBenchController {
	if a == nil || a.benchTerminal == nil {
		return nil
	}
	return a.benchTerminal
}
