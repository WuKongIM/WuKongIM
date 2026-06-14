package top

import (
	"fmt"
	"strings"
	"time"
)

const (
	defaultWindow   = 10 * time.Second
	defaultInterval = time.Second
	defaultView     = "overview"
	defaultLimit    = 20
	minWindow       = 2 * time.Second
)

type config struct {
	ContextDir  string
	ContextName string
	Servers     []string
	Window      time.Duration
	View        string
	Limit       int
	Once        bool
	JSON        bool
	Interval    time.Duration
}

func normalizeConfig(cfg config) (config, error) {
	if cfg.Window == 0 {
		cfg.Window = defaultWindow
	}
	if cfg.Interval == 0 {
		cfg.Interval = defaultInterval
	}
	if strings.TrimSpace(cfg.View) == "" {
		cfg.View = defaultView
	}
	cfg.View = strings.TrimSpace(cfg.View)
	if cfg.Limit == 0 {
		cfg.Limit = defaultLimit
	}
	if cfg.Window < minWindow {
		return cfg, fmt.Errorf("window must be at least %s", minWindow)
	}
	if cfg.Limit <= 0 {
		return cfg, fmt.Errorf("limit must be positive")
	}
	servers := make([]string, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		server = strings.TrimSpace(server)
		if server != "" {
			servers = append(servers, server)
		}
	}
	cfg.Servers = servers
	return cfg, nil
}
