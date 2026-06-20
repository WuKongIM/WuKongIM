package top

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/top/topapi"
	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
)

// topTUIView is the terminal dashboard view model derived from top snapshots.
type topTUIView struct {
	// Header contains the command context shown at the top of the dashboard.
	Header string
	// VerdictLevel is the aggregate health level used for color selection.
	VerdictLevel string
	// VerdictTitle is the compact verdict heading.
	VerdictTitle string
	// VerdictSummary explains the aggregate health level.
	VerdictSummary string
	// VerdictPercent is the ready-node ratio rendered by the gauge.
	VerdictPercent int
	// NodeRows contains the node overview table, including the header row.
	NodeRows [][]string
	// PressureRows contains the hottest pressure signals, including the header row.
	PressureRows [][]string
	// AlertRows contains sticky warnings and errors, including the header row.
	AlertRows [][]string
	// RuntimeRows contains ChannelV2 runtime gauges, including the header row.
	RuntimeRows [][]string
	// StatusRows contains verdict reasons, source status, and operator hints.
	StatusRows []string
}

func buildTUIView(snapshot aggregateSnapshot, cfg config) topTUIView {
	return buildTUIViewWithAlert(snapshot, cfg, 0)
}

func buildTUIViewWithAlert(snapshot aggregateSnapshot, cfg config, selectedAlert int) topTUIView {
	generatedAt := "-"
	if !snapshot.GeneratedAt.IsZero() {
		generatedAt = snapshot.GeneratedAt.Format("2006-01-02 15:04:05 MST")
	}
	header := fmt.Sprintf(
		"WuKongIM top  generated %s  window %s  refresh %s  view %s  alerts %s\nservers: %s",
		generatedAt,
		cfg.Window.String(),
		cfg.Interval.String(),
		cfg.View,
		alertBadge(snapshot),
		topServerSummary(cfg.Servers),
	)
	level := snapshot.Verdict.Level
	if level == "" {
		level = "unknown"
	}
	return topTUIView{
		Header:         header,
		VerdictLevel:   level,
		VerdictTitle:   fmt.Sprintf("VERDICT %s ready %d/%d", level, snapshot.ReadyNodes, snapshot.TotalNodes),
		VerdictSummary: snapshot.Verdict.Summary,
		VerdictPercent: readyPercent(snapshot.ReadyNodes, snapshot.TotalNodes),
		NodeRows:       buildNodeRows(snapshot),
		PressureRows:   buildPressureRows(snapshot),
		AlertRows:      buildAlertRows(snapshot, selectedAlert),
		RuntimeRows:    buildRuntimeRows(snapshot),
		StatusRows:     buildStatusRows(snapshot),
	}
}

func runTermUI(ctx context.Context, cfg config) error {
	if err := ui.Init(); err != nil {
		return fmt.Errorf("initialize termui: %w", err)
	}
	defer ui.Close()

	dashboard := newTopDashboard(cfg)
	snapshot, fetchErr := fetchAggregate(ctx, cfg)
	dashboard.render(snapshot, fetchErr)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	events := ui.PollEvents()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-events:
			switch event.ID {
			case "q", "<C-c>", "<Escape>":
				return nil
			case "r":
				snapshot, fetchErr = fetchAggregate(ctx, cfg)
				dashboard.render(snapshot, fetchErr)
			case "<Down>", "j":
				dashboard.selectNextAlert(snapshot)
				dashboard.render(snapshot, fetchErr)
			case "<Up>", "k":
				dashboard.selectPrevAlert(snapshot)
				dashboard.render(snapshot, fetchErr)
			case "<Enter>", "d":
				dashboard.toggleAlertDetails()
				dashboard.render(snapshot, fetchErr)
			case "<Resize>":
				dashboard.render(snapshot, fetchErr)
			}
		case <-ticker.C:
			snapshot, fetchErr = fetchAggregate(ctx, cfg)
			dashboard.render(snapshot, fetchErr)
		}
	}
}

type topDashboard struct {
	cfg      config
	header   *widgets.Paragraph
	verdict  *widgets.Gauge
	nodes    *widgets.Table
	alerts   *widgets.Table
	pressure *widgets.Table
	runtime  *widgets.Table
	status   *widgets.List
	// selectedAlert is the zero-based index into aggregateAlerts for detail inspection.
	selectedAlert   int
	showAlertDetail bool
}

func newTopDashboard(cfg config) *topDashboard {
	header := widgets.NewParagraph()
	header.Title = " WuKongIM top "
	header.WrapText = true
	header.TextStyle = ui.NewStyle(ui.ColorWhite)
	header.BorderStyle = ui.NewStyle(ui.ColorCyan)

	verdict := widgets.NewGauge()
	verdict.Title = " Verdict "
	verdict.LabelStyle = ui.NewStyle(ui.ColorWhite)
	verdict.BorderStyle = ui.NewStyle(ui.ColorCyan)

	nodes := widgets.NewTable()
	nodes.Title = " Nodes "
	nodes.RowSeparator = false
	nodes.TextStyle = ui.NewStyle(ui.ColorWhite)
	nodes.RowStyles = map[int]ui.Style{0: ui.NewStyle(ui.ColorCyan)}
	nodes.BorderStyle = ui.NewStyle(ui.ColorCyan)

	pressure := widgets.NewTable()
	pressure.Title = " Hot pressure "
	pressure.RowSeparator = false
	pressure.TextStyle = ui.NewStyle(ui.ColorWhite)
	pressure.RowStyles = map[int]ui.Style{0: ui.NewStyle(ui.ColorCyan)}
	pressure.BorderStyle = ui.NewStyle(ui.ColorCyan)

	alerts := widgets.NewTable()
	alerts.Title = " Alerts "
	alerts.RowSeparator = false
	alerts.TextStyle = ui.NewStyle(ui.ColorWhite)
	alerts.RowStyles = map[int]ui.Style{0: ui.NewStyle(ui.ColorCyan)}
	alerts.BorderStyle = ui.NewStyle(ui.ColorCyan)

	runtime := widgets.NewTable()
	runtime.Title = " ChannelV2 runtime "
	runtime.RowSeparator = false
	runtime.TextStyle = ui.NewStyle(ui.ColorWhite)
	runtime.RowStyles = map[int]ui.Style{0: ui.NewStyle(ui.ColorCyan)}
	runtime.BorderStyle = ui.NewStyle(ui.ColorCyan)

	status := widgets.NewList()
	status.Title = " Status "
	status.WrapText = true
	status.TextStyle = ui.NewStyle(ui.ColorWhite)
	status.BorderStyle = ui.NewStyle(ui.ColorCyan)

	return &topDashboard{
		cfg:      cfg,
		header:   header,
		verdict:  verdict,
		nodes:    nodes,
		alerts:   alerts,
		pressure: pressure,
		runtime:  runtime,
		status:   status,
	}
}

func (d *topDashboard) render(snapshot aggregateSnapshot, fetchErr error) {
	d.clampSelectedAlert(snapshot)
	view := buildTUIViewWithAlert(snapshot, d.cfg, d.selectedAlert)
	if fetchErr != nil {
		view.VerdictLevel = "critical"
		view.VerdictTitle = "VERDICT critical fetch error"
		view.VerdictSummary = fetchErr.Error()
		view.VerdictPercent = 0
		view.StatusRows = append([]string{"fetch error: " + fetchErr.Error()}, view.StatusRows...)
	}

	levelColor := topLevelColor(view.VerdictLevel)
	d.header.Text = view.Header + "\nkeys: q/esc/ctrl-c quit, r refresh, up/down select alert, enter/d detail"
	d.verdict.Title = " " + view.VerdictTitle + " "
	d.verdict.Percent = view.VerdictPercent
	d.verdict.BarColor = levelColor
	d.verdict.Label = view.VerdictSummary
	d.nodes.Rows = view.NodeRows
	d.nodes.RowStyles = tableRowStyles(view.NodeRows, 1)
	d.alerts.Rows = view.AlertRows
	d.alerts.RowStyles = tableRowStyles(view.AlertRows, 2)
	d.pressure.Rows = view.PressureRows
	d.pressure.RowStyles = tableRowStyles(view.PressureRows, 2)
	d.runtime.Rows = view.RuntimeRows
	d.runtime.RowStyles = tableRowStyles(view.RuntimeRows, -1)
	if d.showAlertDetail {
		d.status.Title = " Alert detail "
		d.status.Rows = buildAlertDetailRows(snapshot, d.selectedAlert)
	} else {
		d.status.Title = " Status "
		d.status.Rows = view.StatusRows
	}
	if len(d.status.Rows) == 0 {
		d.status.Rows = []string{"no status notes"}
	}
	d.status.TextStyle = ui.NewStyle(levelColor)

	width, height := ui.TerminalDimensions()
	if width <= 0 || height <= 0 {
		return
	}
	d.resizeTables(width)
	grid := ui.NewGrid()
	grid.SetRect(0, 0, width, height)
	if width < 120 {
		grid.Set(
			ui.NewRow(0.14,
				ui.NewCol(0.66, d.header),
				ui.NewCol(0.34, d.verdict),
			),
			ui.NewRow(0.27, d.nodes),
			ui.NewRow(0.21, d.alerts),
			ui.NewRow(0.20, d.pressure),
			ui.NewRow(0.14, d.runtime),
			ui.NewRow(0.14, d.status),
		)
	} else {
		grid.Set(
			ui.NewRow(0.15,
				ui.NewCol(0.66, d.header),
				ui.NewCol(0.34, d.verdict),
			),
			ui.NewRow(0.32, d.nodes),
			ui.NewRow(0.29,
				ui.NewCol(0.55, d.alerts),
				ui.NewCol(0.45, d.pressure),
			),
			ui.NewRow(0.24,
				ui.NewCol(0.58, d.runtime),
				ui.NewCol(0.42, d.status),
			),
		)
	}
	ui.Clear()
	ui.Render(grid)
}

func (d *topDashboard) selectNextAlert(snapshot aggregateSnapshot) {
	count := len(aggregateAlerts(snapshot))
	if count == 0 {
		d.selectedAlert = 0
		return
	}
	d.selectedAlert = (d.selectedAlert + 1) % count
}

func (d *topDashboard) selectPrevAlert(snapshot aggregateSnapshot) {
	count := len(aggregateAlerts(snapshot))
	if count == 0 {
		d.selectedAlert = 0
		return
	}
	d.selectedAlert = (d.selectedAlert - 1 + count) % count
}

func (d *topDashboard) toggleAlertDetails() {
	d.showAlertDetail = !d.showAlertDetail
}

func (d *topDashboard) clampSelectedAlert(snapshot aggregateSnapshot) {
	count := len(aggregateAlerts(snapshot))
	if count == 0 || d.selectedAlert < 0 {
		d.selectedAlert = 0
		return
	}
	if d.selectedAlert >= count {
		d.selectedAlert = count - 1
	}
}

func (d *topDashboard) resizeTables(width int) {
	if width < 120 {
		d.nodes.ColumnWidths = []int{10, 5, 5, 6, 8, 7, 7, 9, 16}
		d.alerts.ColumnWidths = []int{2, 10, 8, 10, 14, 8, 5, 25}
		d.pressure.ColumnWidths = []int{12, 25, 9, 8, 15, 18, 24}
		d.runtime.ColumnWidths = []int{12, 8, 7, 9, 15, 15, 18}
		return
	}
	d.nodes.ColumnWidths = []int{14, 7, 7, 8, 10, 10, 10, 12, 28}
	d.alerts.ColumnWidths = []int{2, 13, 10, 12, 18, 8, 6, 34}
	d.pressure.ColumnWidths = []int{14, 28, 9, 8, 16, 18, 30}
	d.runtime.ColumnWidths = []int{14, 8, 7, 9, 20, 18, 20}
}

func buildNodeRows(snapshot aggregateSnapshot) [][]string {
	rows := [][]string{{"NODE", "READY", "CONN", "CPU%", "MEM", "SEND/s", "ACK/s", "APP_P99", "PRESSURE"}}
	for _, node := range snapshot.Nodes {
		rows = append(rows, []string{
			nodeName(node),
			yesNo(node.Node.Ready),
			formatInt64(connectionCount(node)),
			formatFloat(resourceCPUPercent(node)),
			formatBytes(resourceMemoryAlloc(node)),
			formatFloat(rateValue(node.Traffic, "send")),
			formatFloat(rateValue(node.Traffic, "ack")),
			formatMS(appendP99(node.Traffic)),
			pressureSummary(node),
		})
	}
	if len(rows) == 1 {
		rows = append(rows, []string{"-", "-", "-", "-", "-", "-", "-", "-", "no nodes"})
	}
	return rows
}

func buildPressureRows(snapshot aggregateSnapshot) [][]string {
	rows := [][]string{{"NODE", "SIGNAL", "LEVEL", "SCORE", "LOAD", "P99", "HINT"}}
	for _, node := range snapshot.Nodes {
		if node.Pressure == nil || len(node.Pressure.Top) == 0 {
			continue
		}
		for _, item := range node.Pressure.Top {
			rows = append(rows, []string{
				nodeName(node),
				pressureSignal(item.Component, item.Pool, item.Queue, item.Priority),
				emptyDash(item.Level),
				formatFloat(item.Score),
				pressureLoad(item.Depth, item.Capacity, item.Inflight, item.Workers),
				pressureP99(item.WaitP99MS, item.TaskP99MS),
				emptyDash(item.Hint),
			})
		}
	}
	if len(rows) == 1 {
		rows = append(rows, []string{"-", "-", "ok", "0.00", "-", "-", "no hot pressure"})
	}
	return rows
}

func buildAlertRows(snapshot aggregateSnapshot, selected int) [][]string {
	rows := [][]string{{"", "NODE", "SEVERITY", "COMPONENT", "KIND", "STATE", "COUNT", "MESSAGE"}}
	for i, alert := range aggregateAlerts(snapshot) {
		marker := ""
		if i == selected {
			marker = ">"
		}
		rows = append(rows, []string{
			marker,
			alertNodeName(alert),
			emptyDash(alert.Severity),
			emptyDash(alert.Component),
			emptyDash(alert.Kind),
			alertState(alert),
			formatInt64(int64(alert.Count)),
			emptyDash(alert.Message),
		})
	}
	if len(rows) == 1 {
		rows = append(rows, []string{"", "-", "ok", "-", "-", "-", "0", "no alerts"})
	}
	return rows
}

func buildAlertDetailRows(snapshot aggregateSnapshot, selected int) []string {
	alerts := aggregateAlerts(snapshot)
	if len(alerts) == 0 {
		return []string{"no alert selected"}
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(alerts) {
		selected = len(alerts) - 1
	}
	alert := alerts[selected]
	rows := []string{
		"id: " + emptyDash(alert.ID),
		"node: " + alertNodeName(alert),
		"severity: " + emptyDash(alert.Severity),
		"component: " + emptyDash(alert.Component),
		"kind: " + emptyDash(alert.Kind),
		"state: " + alertState(alert),
		"count: " + formatInt64(int64(alert.Count)),
		"first_seen: " + formatAlertTime(alert.FirstSeen),
		"last_seen: " + formatAlertTime(alert.LastSeen),
	}
	if alert.ResolvedAt != nil {
		rows = append(rows, "resolved_at: "+formatAlertTime(*alert.ResolvedAt))
	}
	rows = append(rows,
		"message: "+emptyDash(alert.Message),
		"hint: "+emptyDash(alert.Hint),
	)
	if len(alert.Evidence) > 0 {
		rows = append(rows, "evidence:")
		keys := make([]string, 0, len(alert.Evidence))
		for key := range alert.Evidence {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows = append(rows, "  "+key+": "+alert.Evidence[key])
		}
	}
	if alert.Fingerprint != "" {
		rows = append(rows, "fingerprint: "+alert.Fingerprint)
	}
	return rows
}

func alertBadge(snapshot aggregateSnapshot) string {
	active := 0
	recent := 0
	warnings := 0
	errors := 0
	critical := 0
	for _, node := range snapshot.Nodes {
		if node.Alerts == nil {
			continue
		}
		active += node.Alerts.Counts.Active
		recent += node.Alerts.Counts.Recent
		warnings += node.Alerts.Counts.Warning
		errors += node.Alerts.Counts.Error
		critical += node.Alerts.Counts.Critical
	}
	if active == 0 && recent == 0 {
		return "none"
	}
	return fmt.Sprintf("C:%d E:%d W:%d active:%d recent:%d", critical, errors, warnings, active, recent)
}

func buildRuntimeRows(snapshot aggregateSnapshot) [][]string {
	rows := [][]string{{"NODE", "ACTIVE", "L/F", "MAILBOX", "WORKER_Q", "INFLIGHT", "HOT_STAGE"}}
	for _, node := range snapshot.Nodes {
		if node.ChannelV2 == nil {
			continue
		}
		ch := node.ChannelV2
		rows = append(rows, []string{
			nodeName(node),
			formatInt64(ch.ActiveTotal),
			fmt.Sprintf("%s/%s", formatInt64(ch.ActiveLeader), formatInt64(ch.ActiveFollower)),
			formatInt64(ch.ReactorMailboxDepthMax),
			formatInt64Map(ch.WorkerQueueDepthByPool, ch.WorkerQueueCapacityByPool),
			formatInt64Map(ch.WorkerInflightByPool, ch.WorkerCapacityByPool),
			hotStageLabel(ch.HotStage, ch.StageP99MS, ch.AppendP99MS),
		})
	}
	if len(rows) == 1 {
		rows = append(rows, []string{"-", "-", "-", "-", "-", "-", "no channelv2 data"})
	}
	return rows
}

func buildStatusRows(snapshot aggregateSnapshot) []string {
	var rows []string
	if snapshot.Verdict.Summary != "" {
		rows = append(rows, "summary: "+snapshot.Verdict.Summary)
	}
	for _, reason := range snapshot.Verdict.Reasons {
		rows = append(rows, "reason: "+reason)
	}
	rows = append(rows,
		fmt.Sprintf("ready: %d/%d", snapshot.ReadyNodes, snapshot.TotalNodes),
		fmt.Sprintf("collector: %s", sourceStatus(snapshot.Nodes, func(node accessapi.TopSnapshot) bool {
			return node.Sources.Collector.Available
		})),
		fmt.Sprintf("cluster snapshot: %s", sourceStatus(snapshot.Nodes, func(node accessapi.TopSnapshot) bool {
			return node.Sources.ClusterSnapshot.Available
		})),
		fmt.Sprintf("metrics: %s", metricsStatus(snapshot)),
	)
	for _, node := range snapshot.Nodes {
		for _, note := range node.Sources.Notes {
			rows = append(rows, nodeName(node)+": "+note)
		}
	}
	return rows
}

func sourceStatus(nodes []accessapi.TopSnapshot, available func(accessapi.TopSnapshot) bool) string {
	if len(nodes) == 0 {
		return "unknown"
	}
	ok := 0
	for _, node := range nodes {
		if available(node) {
			ok++
		}
	}
	return fmt.Sprintf("%d/%d available", ok, len(nodes))
}

func metricsStatus(snapshot aggregateSnapshot) string {
	if len(snapshot.Nodes) == 0 {
		return "unknown"
	}
	enabled := 0
	required := 0
	for _, node := range snapshot.Nodes {
		if node.Sources.Metrics.Enabled {
			enabled++
		}
		if node.Sources.Metrics.Required {
			required++
		}
	}
	if required == 0 {
		return fmt.Sprintf("%d/%d enabled, optional", enabled, len(snapshot.Nodes))
	}
	return fmt.Sprintf("%d/%d enabled, required by %d", enabled, len(snapshot.Nodes), required)
}

func topServerSummary(servers []string) string {
	if len(servers) == 0 {
		return "-"
	}
	if len(servers) <= 3 {
		return strings.Join(servers, ", ")
	}
	return strings.Join(servers[:3], ", ") + fmt.Sprintf(", +%d more", len(servers)-3)
}

func readyPercent(ready, total int) int {
	if total <= 0 {
		return 0
	}
	return int(float64(ready) / float64(total) * 100)
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func pressureSignal(component, pool, queue, priority string) string {
	var parts []string
	if component != "" {
		parts = append(parts, component)
	}
	var resourceParts []string
	for _, value := range []string{pool, queue} {
		if value != "" {
			resourceParts = append(resourceParts, value)
		}
	}
	if priority != "" && priority != "none" {
		resourceParts = append(resourceParts, priority)
	}
	if len(resourceParts) > 0 {
		parts = append(parts, strings.Join(resourceParts, "/"))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func formatCapacity(value, capacity int64) string {
	if value <= 0 && capacity <= 0 {
		return "-"
	}
	if capacity <= 0 {
		return formatInt64(value) + "/-"
	}
	return formatInt64(value) + "/" + formatInt64(capacity)
}

func pressureLoad(depth, capacity, inflight, workers int64) string {
	var parts []string
	if depth > 0 || capacity > 0 {
		parts = append(parts, "depth "+formatCapacity(depth, capacity))
	}
	if inflight > 0 || workers > 0 {
		parts = append(parts, "inflight "+formatCapacity(inflight, workers))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func pressureP99(waitMS, taskMS float64) string {
	var parts []string
	if waitMS > 0 {
		parts = append(parts, "wait "+formatMS(waitMS))
	}
	if taskMS > 0 {
		parts = append(parts, "task "+formatMS(taskMS))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func formatInt64Map(values, capacities map[string]int64) string {
	if len(values) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := formatInt64(values[key])
		if capacity, ok := capacities[key]; ok && capacity > 0 {
			value += "/" + formatInt64(capacity)
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ",")
}

func hotStageLabel(stage string, p99ByStage map[string]float64, appendP99MS float64) string {
	if stage != "" {
		if value := p99ByStage[stage]; value > 0 {
			return stage + " " + formatMS(value)
		}
		return stage
	}
	if appendP99MS > 0 {
		return "append " + formatMS(appendP99MS)
	}
	return "-"
}

func formatAlertTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func topLevelColor(level string) ui.Color {
	switch level {
	case "critical":
		return ui.ColorRed
	case "error":
		return ui.ColorRed
	case "degraded":
		return ui.ColorMagenta
	case "warn":
		return ui.ColorYellow
	case "busy":
		return ui.ColorYellow
	case "ok":
		return ui.ColorGreen
	default:
		return ui.ColorWhite
	}
}

func tableRowStyles(rows [][]string, levelColumn int) map[int]ui.Style {
	styles := map[int]ui.Style{0: ui.NewStyle(ui.ColorCyan)}
	if levelColumn < 0 {
		return styles
	}
	for i := 1; i < len(rows); i++ {
		if levelColumn >= len(rows[i]) {
			continue
		}
		styles[i] = ui.NewStyle(topLevelColor(rows[i][levelColumn]))
	}
	return styles
}
