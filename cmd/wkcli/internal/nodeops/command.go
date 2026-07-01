package nodeops

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
	"github.com/spf13/cobra"
)

type commandConfig struct {
	Server      string
	ContextName string
	Token       string
	Timeout     time.Duration
	JSON        bool
}

type nodeListResponse struct {
	Items []nodeListItem `json:"items"`
	Total int            `json:"total"`
}

type nodeListItem struct {
	NodeID     uint64 `json:"node_id"`
	Name       string `json:"name"`
	Membership struct {
		JoinState   string `json:"join_state"`
		Schedulable bool   `json:"schedulable"`
	} `json:"membership"`
	Health struct {
		Fresh                   bool   `json:"fresh"`
		Freshness               string `json:"freshness"`
		Status                  string `json:"status"`
		RuntimeReady            bool   `json:"runtime_ready"`
		ObservedControlRevision uint64 `json:"observed_control_revision"`
	} `json:"health"`
}

// nodeDiagnosticsResponse keeps the manager-only fields needed for human output.
type nodeDiagnosticsResponse struct {
	NodeID uint64 `json:"node_id"`
	Node   struct {
		Membership struct {
			JoinState string `json:"join_state"`
		} `json:"membership"`
	} `json:"node"`
	Summary struct {
		SafeToRemove          bool   `json:"safe_to_remove"`
		RecommendedNextAction string `json:"recommended_next_action"`
		ActiveTaskCount       int    `json:"active_task_count"`
	} `json:"summary"`
	ActiveTasks []nodeDiagnosticTask  `json:"active_tasks"`
	TaskAudits  []nodeDiagnosticAudit `json:"task_audits"`
	Slots       []nodeDiagnosticSlot  `json:"slots"`
	Warnings    []string              `json:"warnings"`
}

type nodeDiagnosticTask struct {
	TaskID              string   `json:"task_id"`
	Kind                string   `json:"kind"`
	Step                string   `json:"step"`
	Status              string   `json:"status"`
	PhaseIndex          int      `json:"phase_index"`
	ObservedConfigIndex uint64   `json:"observed_config_index"`
	ObservedVoters      []uint64 `json:"observed_voters"`
}

type nodeDiagnosticAudit struct {
	TaskID     string `json:"task_id"`
	LastReason string `json:"last_reason"`
}

type nodeDiagnosticSlot struct {
	SlotID          uint32   `json:"slot_id"`
	DesiredPeers    []uint64 `json:"desired_peers"`
	PreferredLeader uint64   `json:"preferred_leader"`
}

// NewCommand builds manager-backed node operation commands.
func NewCommand(deps command.Deps) *cobra.Command {
	cfg := commandConfig{Timeout: defaultTimeout}
	cmd := &cobra.Command{
		Use:          "node",
		Short:        "Operate WuKongIM cluster nodes",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return command.Exit{Code: command.ExitInternal, Message: err.Error()}
			}
			return command.Exit{Code: command.ExitConfig}
		},
	}
	cmd.SetOut(deps.Stdout)
	cmd.SetErr(deps.Stderr)
	cmd.PersistentFlags().StringVar(&cfg.Server, "server", "", "Manager HTTP server URL")
	cmd.PersistentFlags().StringVar(&cfg.ContextName, "context", "", "Named wkcli context to read a manager server from")
	cmd.PersistentFlags().StringVar(&cfg.Token, "token", "", "Manager bearer token")
	cmd.PersistentFlags().DurationVar(&cfg.Timeout, "timeout", defaultTimeout, "HTTP request timeout")
	cmd.PersistentFlags().BoolVar(&cfg.JSON, "json", false, "Render raw JSON output")
	cmd.AddCommand(newNodeListCommand(deps, &cfg), newActivateCommand(deps, &cfg), newDiagnoseCommand(deps, &cfg), newOnboardingCommand(deps, &cfg), newScaleInCommand(deps, &cfg))
	return cmd
}

func newNodeListCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List cluster nodes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if cfg.JSON {
				var out any
				if err := client.ListNodes(cmd.Context(), &out); err != nil {
					return clientExit(err)
				}
				return writePrettyJSON(deps.Stdout, out)
			}
			var out nodeListResponse
			if err := client.ListNodes(cmd.Context(), &out); err != nil {
				return clientExit(err)
			}
			printNodeList(deps.Stdout, out)
			return nil
		},
	}
}

func newActivateCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "activate NODE_ID",
		Short: "Activate a joined node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeID(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if cfg.JSON {
				var out map[string]any
				if err := client.doJSON(cmd.Context(), http.MethodPost, nodePath(nodeID, "activate"), nil, &out); err != nil {
					return clientExit(err)
				}
				return writePrettyJSON(deps.Stdout, out)
			}
			out, err := client.ActivateNode(cmd.Context(), nodeID)
			if err != nil {
				return clientExit(err)
			}
			printLifecycle(deps.Stdout, out)
			return nil
		},
	}
}

func newDiagnoseCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	req := DiagnosticRequest{
		TaskLimit:  20,
		AuditLimit: 10,
		SlotLimit:  256,
	}
	cmd := &cobra.Command{
		Use:   "diagnose NODE_ID",
		Short: "Inspect dynamic node root-cause diagnostics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeID(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if err := validateDiagnosticRequest(req); err != nil {
				return err
			}
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if cfg.JSON {
				var out any
				if err := client.DynamicNodeDiagnostics(cmd.Context(), nodeID, req, &out); err != nil {
					return clientExit(err)
				}
				return writePrettyJSON(deps.Stdout, out)
			}
			var out nodeDiagnosticsResponse
			if err := client.DynamicNodeDiagnostics(cmd.Context(), nodeID, req, &out); err != nil {
				return clientExit(err)
			}
			printNodeDiagnostics(deps.Stdout, out)
			return nil
		},
	}
	cmd.Flags().IntVar(&req.TaskLimit, "task-limit", 20, "Maximum active tasks to request")
	cmd.Flags().IntVar(&req.AuditLimit, "audit-limit", 10, "Maximum task audits to request")
	cmd.Flags().IntVar(&req.SlotLimit, "slot-limit", 256, "Maximum slots to request")
	return cmd
}

func newOnboardingCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "onboarding",
		Short:        "Operate node onboarding",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return command.Exit{Code: command.ExitInternal, Message: err.Error()}
			}
			return command.Exit{Code: command.ExitConfig}
		},
	}
	cmd.AddCommand(
		newOnboardingMoveCommand(deps, cfg, "plan"),
		newOnboardingMoveCommand(deps, cfg, "start"),
		newOnboardingMoveCommand(deps, cfg, "advance"),
		newOnboardingStatusCommand(deps, cfg),
	)
	return cmd
}

func newOnboardingMoveCommand(deps command.Deps, cfg *commandConfig, action string) *cobra.Command {
	var maxSlotMoves uint32 = 1
	cmd := &cobra.Command{
		Use:   action + " NODE_ID",
		Short: "Run onboarding " + action + " for a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeID(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if err := validateMaxSlotMoves(maxSlotMoves); err != nil {
				return err
			}
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			out := map[string]any{}
			switch action {
			case "plan":
				err = client.OnboardingPlan(cmd.Context(), nodeID, maxSlotMoves, &out)
			case "start":
				err = client.OnboardingStart(cmd.Context(), nodeID, maxSlotMoves, &out)
			case "advance":
				err = client.OnboardingAdvance(cmd.Context(), nodeID, maxSlotMoves, &out)
			default:
				err = fmt.Errorf("unknown onboarding action %q", action)
			}
			if err != nil {
				return clientExit(err)
			}
			return writePrettyJSON(deps.Stdout, out)
		},
	}
	cmd.Flags().Uint32Var(&maxSlotMoves, "max-slot-moves", 1, "Maximum slot moves to request")
	return cmd
}

func newOnboardingStatusCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "status NODE_ID",
		Short: "Show onboarding status for a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeID(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			out := map[string]any{}
			if err := client.OnboardingStatus(cmd.Context(), nodeID, &out); err != nil {
				return clientExit(err)
			}
			return writePrettyJSON(deps.Stdout, out)
		},
	}
}

func newScaleInCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "scale-in",
		Short:        "Operate node scale-in lifecycle",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return command.Exit{Code: command.ExitInternal, Message: err.Error()}
			}
			return command.Exit{Code: command.ExitConfig}
		},
	}
	cmd.AddCommand(
		newScaleInStartCommand(deps, cfg),
		newScaleInMoveCommand(deps, cfg, "plan"),
		newScaleInMoveCommand(deps, cfg, "advance"),
		newScaleInDrainCommand(deps, cfg),
		newScaleInStatusCommand(deps, cfg),
		newScaleInRemoveCommand(deps, cfg),
	)
	return cmd
}

func newScaleInStartCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "start NODE_ID",
		Short: "Start node scale-in",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeID(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if cfg.JSON {
				var out map[string]any
				if err := client.doJSON(cmd.Context(), http.MethodPost, nodePath(nodeID, "scale-in/start"), nil, &out); err != nil {
					return clientExit(err)
				}
				return writePrettyJSON(deps.Stdout, out)
			}
			out, err := client.ScaleInStart(cmd.Context(), nodeID)
			if err != nil {
				return clientExit(err)
			}
			printLifecycle(deps.Stdout, out)
			return nil
		},
	}
}

func newScaleInMoveCommand(deps command.Deps, cfg *commandConfig, action string) *cobra.Command {
	var maxSlotMoves uint32 = 1
	cmd := &cobra.Command{
		Use:   action + " NODE_ID",
		Short: "Run scale-in " + action + " for a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeID(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if err := validateMaxSlotMoves(maxSlotMoves); err != nil {
				return err
			}
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			out := map[string]any{}
			switch action {
			case "plan":
				err = client.ScaleInPlan(cmd.Context(), nodeID, maxSlotMoves, &out)
			case "advance":
				err = client.ScaleInAdvance(cmd.Context(), nodeID, maxSlotMoves, &out)
			default:
				err = fmt.Errorf("unknown scale-in action %q", action)
			}
			if err != nil {
				return clientExit(err)
			}
			return writePrettyJSON(deps.Stdout, out)
		},
	}
	cmd.Flags().Uint32Var(&maxSlotMoves, "max-slot-moves", 1, "Maximum slot moves to request")
	return cmd
}

func newScaleInDrainCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	var draining bool = true
	cmd := &cobra.Command{
		Use:   "drain NODE_ID",
		Short: "Set node scale-in gateway draining",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeID(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			out := map[string]any{}
			if err := client.SetScaleInDrain(cmd.Context(), nodeID, draining, &out); err != nil {
				return clientExit(err)
			}
			if cfg.JSON {
				return writePrettyJSON(deps.Stdout, out)
			}
			printScaleInDrain(deps.Stdout, out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&draining, "draining", true, "Whether the node should drain gateway sessions")
	return cmd
}

func newScaleInStatusCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "status NODE_ID",
		Short: "Show scale-in safety status for a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeID(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if cfg.JSON {
				var out any
				if err := client.doJSON(cmd.Context(), http.MethodGet, nodePath(nodeID, "scale-in/status"), nil, &out); err != nil {
					return clientExit(err)
				}
				return writePrettyJSON(deps.Stdout, out)
			}
			out, err := client.ScaleInStatus(cmd.Context(), nodeID)
			if err != nil {
				return clientExit(err)
			}
			printScaleInStatus(deps.Stdout, out)
			return nil
		},
	}
}

func newScaleInRemoveCommand(deps command.Deps, cfg *commandConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "remove NODE_ID",
		Short: "Remove a fully drained scale-in node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeID(args[0])
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			client, err := clientFromConfig(deps, *cfg)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			if cfg.JSON {
				var out map[string]any
				if err := client.doJSON(cmd.Context(), http.MethodPost, nodePath(nodeID, "scale-in/remove"), nil, &out); err != nil {
					return clientExit(err)
				}
				return writePrettyJSON(deps.Stdout, out)
			}
			out, err := client.RemoveScaleInNode(cmd.Context(), nodeID)
			if err != nil {
				return clientExit(err)
			}
			printLifecycle(deps.Stdout, out)
			return nil
		},
	}
}

func clientFromConfig(deps command.Deps, cfg commandConfig) (*Client, error) {
	server, err := resolveManagerServer(deps, cfg)
	if err != nil {
		return nil, err
	}
	return NewClient(Config{Server: server, Token: cfg.Token, Timeout: cfg.Timeout}), nil
}

func resolveManagerServer(deps command.Deps, cfg commandConfig) (string, error) {
	if server := strings.TrimSpace(cfg.Server); server != "" {
		if err := validateManagerServer(server); err != nil {
			return "", err
		}
		return server, nil
	}

	store := contextcmd.NewStore(contextDirFromDeps(deps))
	name := strings.TrimSpace(cfg.ContextName)
	if name == "" {
		current, err := store.Current()
		if err != nil {
			return "", err
		}
		name = current
	}
	if name == "" {
		return "", fmt.Errorf("no manager server configured; pass --server, --context, or select a wkcli context")
	}
	ctx, err := store.Load(name)
	if err != nil {
		return "", err
	}
	servers := splitServerValues(ctx.Servers)
	if len(servers) == 0 {
		return "", fmt.Errorf("context %s has no manager servers", name)
	}
	server := servers[0]
	if len(servers) > 1 && deps.Stderr != nil {
		fmt.Fprintf(deps.Stderr, "using first manager server from context %s: %s\n", name, server)
	}
	return server, nil
}

func contextDirFromDeps(deps command.Deps) string {
	if deps.ContextDir == nil || strings.TrimSpace(*deps.ContextDir) == "" {
		return contextcmd.DefaultStoreDir()
	}
	return *deps.ContextDir
}

func validateManagerServer(server string) error {
	parsed, err := url.Parse(server)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("manager server %q must be an absolute http or https URL", server)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("manager server %q must use http or https", server)
	}
	return nil
}

func splitServerValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			server := strings.TrimSpace(part)
			if server != "" {
				out = append(out, server)
			}
		}
	}
	return out
}

func parseNodeID(raw string) (uint64, error) {
	nodeID, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || nodeID == 0 {
		return 0, fmt.Errorf("NODE_ID must be a positive integer")
	}
	return nodeID, nil
}

func validateMaxSlotMoves(value uint32) error {
	if value < 1 || value > 5 {
		return command.Exit{Code: command.ExitConfig, Message: "--max-slot-moves must be between 1 and 5"}
	}
	return nil
}

func validateDiagnosticRequest(req DiagnosticRequest) error {
	if req.TaskLimit < 1 || req.TaskLimit > 50 {
		return command.Exit{Code: command.ExitConfig, Message: "--task-limit must be between 1 and 50"}
	}
	if req.AuditLimit < 1 || req.AuditLimit > 20 {
		return command.Exit{Code: command.ExitConfig, Message: "--audit-limit must be between 1 and 20"}
	}
	if req.SlotLimit < 1 || req.SlotLimit > 256 {
		return command.Exit{Code: command.ExitConfig, Message: "--slot-limit must be between 1 and 256"}
	}
	return nil
}

func clientExit(err error) error {
	var apiErr *APIError
	if AsAPIError(err, &apiErr) {
		return command.Exit{Code: command.ExitUnavailable, Message: apiErr.Error()}
	}
	return command.Exit{Code: command.ExitInternal, Message: err.Error()}
}

func writePrettyJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return command.Exit{Code: command.ExitInternal, Message: err.Error()}
	}
	return nil
}

func printNodeList(w io.Writer, out nodeListResponse) {
	for _, item := range out.Items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = fmt.Sprintf("node-%d", item.NodeID)
		}
		fmt.Fprintf(w, "%s node=%d join_state=%s schedulable=%t health=%s/%s fresh=%t runtime_ready=%t control_rev=%d\n",
			name,
			item.NodeID,
			dash(item.Membership.JoinState),
			item.Membership.Schedulable,
			dash(item.Health.Freshness),
			dash(item.Health.Status),
			item.Health.Fresh,
			item.Health.RuntimeReady,
			item.Health.ObservedControlRevision,
		)
	}
}

func printLifecycle(w io.Writer, out LifecycleResponse) {
	fmt.Fprintf(w, "node=%d changed=%t join_state=%s revision=%d\n",
		out.NodeID,
		out.Changed,
		dash(out.JoinState),
		out.Revision,
	)
}

func printScaleInDrain(w io.Writer, out map[string]any) {
	fmt.Fprintf(w, "draining=%s accepting_new_sessions=%s gateway_sessions=%s active_online=%s closing_online=%s total_online=%s pending_activations=%s unknown=%s\n",
		mapValue(out, "draining"),
		mapValue(out, "accepting_new_sessions"),
		mapValue(out, "gateway_sessions"),
		mapValue(out, "active_online"),
		mapValue(out, "closing_online"),
		mapValue(out, "total_online"),
		mapValue(out, "pending_activations"),
		mapValue(out, "unknown"),
	)
}

func mapValue(out map[string]any, key string) string {
	value, ok := out[key]
	if !ok || value == nil {
		return "-"
	}
	return fmt.Sprint(value)
}

func printScaleInStatus(w io.Writer, status NodeScaleInStatus) {
	blockedReasons := "-"
	if len(status.BlockedReasons) > 0 {
		blockedReasons = strings.Join(status.BlockedReasons, ",")
	}
	fmt.Fprintf(w, "node=%d join_state=%s state_revision=%d\n", status.NodeID, dash(status.JoinState), status.StateRevision)
	fmt.Fprintf(w, "safe_to_proceed=%t safe_to_remove=%t\n", status.SafeToProceed, status.SafeToRemove)
	fmt.Fprintf(w, "blocked_reasons=%s\n", blockedReasons)
	fmt.Fprintf(w, "health=%s/%s fresh=%t control_rev=%d/%d\n",
		dash(status.HealthFreshness),
		dash(status.HealthStatus),
		status.HealthFresh,
		status.ObservedControlRevision,
		status.RequiredControlRevision,
	)
	fmt.Fprintf(w, "slots replicas=%d leaders=%d tasks active=%d failed=%d channels leader=%d replica=%d isr=%d\n",
		status.SlotReplicaCount,
		status.SlotLeaderCount,
		status.ActiveTaskCount,
		status.FailedTaskCount,
		status.ChannelLeaderCount,
		status.ChannelReplicaCount,
		status.ChannelISRCount,
	)
	fmt.Fprintf(w, "gateway draining=%t accepting_new_sessions=%t gateway_sessions=%d active_online=%d closing_online=%d total_online=%d pending_activations=%d\n",
		status.GatewayDraining,
		status.AcceptingNewSessions,
		status.GatewaySessions,
		status.ActiveOnline,
		status.ClosingOnline,
		status.TotalOnline,
		status.PendingActivations,
	)
	fmt.Fprintf(w, "missing_node=%t join_state_blocked=%t controller_role=%t data_role=%t blocked_by_health=%t blocked_by_runtime_drain=%t blocked_by_stale_revision=%t blocked_by_control_revision=%t blocked_by_slots=%t blocked_by_slot_leadership=%t blocked_by_slot_runtime=%t blocked_by_tasks=%t blocked_by_channels=%t unknown_runtime=%t unknown_control_revision=%t unknown_channel_inventory=%t health_report_age_ms=%d health_report_ttl_ms=%d\n",
		status.BlockedByMissingNode,
		status.BlockedByJoinState,
		status.BlockedByControllerRole,
		status.BlockedByDataRole,
		status.BlockedByHealth,
		status.BlockedByRuntimeDrain,
		status.BlockedByStaleRevision,
		status.BlockedByControlRevision,
		status.BlockedBySlots,
		status.BlockedBySlotLeadership,
		status.BlockedBySlotRuntime,
		status.BlockedByTasks,
		status.BlockedByChannels,
		status.UnknownRuntime || status.RuntimeUnknown,
		status.UnknownControlRevision,
		status.UnknownChannelInventory,
		status.HealthReportAgeMS,
		status.HealthReportTTLMS,
	)
}

func printNodeDiagnostics(w io.Writer, out nodeDiagnosticsResponse) {
	fmt.Fprintf(w, "node=%d join_state=%s safe_to_remove=%t recommended_next_action=%s active_tasks=%d task_audits=%d slots=%d warnings=%d\n",
		out.NodeID,
		dash(out.Node.Membership.JoinState),
		out.Summary.SafeToRemove,
		dash(out.Summary.RecommendedNextAction),
		diagnosticActiveTaskCount(out),
		len(out.TaskAudits),
		len(out.Slots),
		len(out.Warnings),
	)
	for _, task := range out.ActiveTasks {
		fmt.Fprintf(w, "task=%s kind=%s step=%s status=%s phase_index=%d observed_config_index=%d observed_voters=%v\n",
			dash(task.TaskID),
			dash(task.Kind),
			dash(task.Step),
			dash(task.Status),
			task.PhaseIndex,
			task.ObservedConfigIndex,
			task.ObservedVoters,
		)
	}
	for _, audit := range out.TaskAudits {
		fmt.Fprintf(w, "audit=%s last_reason=%s\n", dash(audit.TaskID), dash(audit.LastReason))
	}
	for _, slot := range out.Slots {
		fmt.Fprintf(w, "slot=%d desired_peers=%v preferred_leader=%d\n", slot.SlotID, slot.DesiredPeers, slot.PreferredLeader)
	}
	for _, warning := range out.Warnings {
		fmt.Fprintf(w, "warning=%s\n", dash(warning))
	}
}

func diagnosticActiveTaskCount(out nodeDiagnosticsResponse) int {
	if out.Summary.ActiveTaskCount > 0 {
		return out.Summary.ActiveTaskCount
	}
	return len(out.ActiveTasks)
}

func dash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
