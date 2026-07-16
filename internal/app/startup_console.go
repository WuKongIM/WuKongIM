package app

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
)

const (
	startupConsoleLabelWidth        = 11
	startupFailureComponentMaxRunes = 48
	startupFailureReasonMaxRunes    = 240
)

// startupConsoleSnapshot is the single effective runtime view shared by lifecycle logs and console output.
type startupConsoleSnapshot struct {
	// nodeID is the effective cluster node identity.
	nodeID uint64
	// clusterID is the observed cluster identity when available.
	clusterID string
	// deployment is single-node-cluster or multi-node-cluster.
	deployment string
	// clusterListen is the observed cluster listener address.
	clusterListen string
	// apiListen is the observed API listener address.
	apiListen string
	// managerListen is the observed manager listener address.
	managerListen string
	// gatewayListeners contains named, observed client listener addresses.
	gatewayListeners []startupGatewayListener
	// metricsEnabled reports whether the API metrics endpoint is enabled.
	metricsEnabled bool
	// dataDir is the effective node data root.
	dataDir string
}

// startupGatewayListener keeps one configured name paired with its effective bound address.
type startupGatewayListener struct {
	// name is the configured listener name.
	name string
	// address is the normalized listener URL.
	address string
}

// startupConsole renders bounded, human-facing lifecycle information to one writer.
type startupConsole struct {
	// writer receives complete startup lifecycle blocks.
	writer io.Writer
	// color enables ANSI accents for an interactive terminal.
	color bool
}

// WithStartupConsole overrides the startup console writer and terminal-color capability.
// It is intended for command wiring and black-box lifecycle tests, not product configuration.
func WithStartupConsole(writer io.Writer, terminal bool) Option {
	return func(a *App) {
		if writer == nil {
			return
		}
		a.startupConsole = &startupConsole{writer: writer, color: terminal}
	}
}

func (c *startupConsole) starting(snapshot startupConsoleSnapshot) {
	if c == nil || c.writer == nil {
		return
	}
	var out strings.Builder
	if c.color {
		out.WriteString("\x1b[1;36m")
	}
	fmt.Fprintf(&out, "WuKongIM · Starting node %d...", snapshot.nodeID)
	if c.color {
		out.WriteString("\x1b[0m")
	}
	out.WriteString("\n\n")
	_, _ = io.WriteString(c.writer, out.String())
}

func (c *startupConsole) ready(snapshot startupConsoleSnapshot, duration time.Duration) {
	if c == nil || c.writer == nil {
		return
	}
	var out strings.Builder
	if c.color {
		out.WriteString("\x1b[1;36m")
	}
	out.WriteString("WuKongIM")
	if c.color {
		out.WriteString("\x1b[0m")
	}
	out.WriteString("\n\n")
	writeStartupConsoleRow(&out, "Node", fmt.Sprintf("%d · %s", snapshot.nodeID, humanDeployment(snapshot.deployment)))
	writeStartupConsoleRow(&out, "Cluster", joinStartupValues(snapshot.clusterID, snapshot.clusterListen))
	writeGatewayConsoleRows(&out, snapshot.gatewayListeners)
	writeStartupConsoleRow(&out, "API", httpConsoleAddress(snapshot.apiListen))
	writeStartupConsoleRow(&out, "Manager", httpConsoleAddress(snapshot.managerListen))
	metrics := "disabled"
	if snapshot.metricsEnabled && strings.TrimSpace(snapshot.apiListen) != "" {
		metrics = strings.TrimRight(httpConsoleAddress(snapshot.apiListen), "/") + "/metrics"
	}
	writeStartupConsoleRow(&out, "Metrics", metrics)
	writeStartupConsoleRow(&out, "Data", disabledWhenEmpty(snapshot.dataDir))
	out.WriteByte('\n')
	if c.color {
		out.WriteString("\x1b[1;32m")
	}
	fmt.Fprintf(&out, "✓ Ready in %s", startupDurationText(duration))
	if c.color {
		out.WriteString("\x1b[0m")
	}
	out.WriteByte('\n')
	_, _ = io.WriteString(c.writer, out.String())
}

func (c *startupConsole) failed(component string, err error) {
	if c == nil || c.writer == nil || err == nil {
		return
	}
	var out strings.Builder
	if c.color {
		out.WriteString("\x1b[1;31m")
	}
	component = disabledWhenEmpty(boundedConsoleText(component, startupFailureComponentMaxRunes))
	reason := disabledWhenEmpty(boundedConsoleText(err.Error(), startupFailureReasonMaxRunes))
	fmt.Fprintf(&out, "✗ Startup failed at %s: %s", component, reason)
	if c.color {
		out.WriteString("\x1b[0m")
	}
	out.WriteByte('\n')
	_, _ = io.WriteString(c.writer, out.String())
}

func writeStartupConsoleRow(out *strings.Builder, label, value string) {
	fmt.Fprintf(out, "%-*s%s\n", startupConsoleLabelWidth, label, disabledWhenEmpty(value))
}

func writeGatewayConsoleRows(out *strings.Builder, listeners []startupGatewayListener) {
	if len(listeners) == 0 {
		writeStartupConsoleRow(out, "Gateway", "disabled")
		return
	}
	type listenerRow struct {
		name string
		addr string
	}
	rows := make([]listenerRow, 0, len(listeners))
	nameWidth := 0
	for _, listener := range listeners {
		name := strings.TrimSpace(listener.name)
		if name == "" {
			name = "listener"
		}
		addr := strings.TrimSpace(listener.address)
		if len(name) > nameWidth {
			nameWidth = len(name)
		}
		rows = append(rows, listenerRow{name: name, addr: disabledWhenEmpty(addr)})
	}
	for index, row := range rows {
		label := ""
		if index == 0 {
			label = "Gateway"
		}
		writeStartupConsoleRow(out, label, fmt.Sprintf("%-*s   %s", nameWidth, row.name, row.addr))
	}
}

func humanDeployment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return strings.ReplaceAll(value, "-cluster", " cluster")
}

func joinStartupValues(first, second string) string {
	first = strings.TrimSpace(first)
	second = strings.TrimSpace(second)
	switch {
	case first == "" && second == "":
		return "disabled"
	case first == "":
		return second
	case second == "":
		return first
	default:
		return first + " · " + second
	}
}

func httpConsoleAddress(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "disabled"
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return value
	}
	return "http://" + value
}

func disabledWhenEmpty(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "disabled"
	}
	return value
}

func startupDurationText(duration time.Duration) string {
	if duration < time.Millisecond {
		return "<1ms"
	}
	return duration.Round(time.Millisecond).String()
}

func boundedConsoleText(value string, maxRunes int) string {
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}
