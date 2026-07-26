package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/spf13/cobra"
)

const maxResponseBytes = 4 << 20

type config struct {
	server      string
	contextName string
	token       string
	timeout     time.Duration
	rawJSON     bool
}

// NewCommand builds the Manager-backed cluster backup command tree.
func NewCommand(deps command.Deps) *cobra.Command {
	cfg := config{timeout: 30 * time.Second}
	cmd := &cobra.Command{Use: "backup", Short: "Operate cluster-semantic backup", Args: cobra.NoArgs, SilenceUsage: true}
	cmd.SetOut(deps.Stdout)
	cmd.SetErr(deps.Stderr)
	cmd.PersistentFlags().StringVar(&cfg.server, "server", "", "Manager HTTP server URL")
	cmd.PersistentFlags().StringVar(&cfg.contextName, "context", "", "Named wkcli context")
	cmd.PersistentFlags().StringVar(&cfg.token, "token", "", "Manager bearer token")
	cmd.PersistentFlags().DurationVar(&cfg.timeout, "timeout", cfg.timeout, "Manager request timeout")
	cmd.PersistentFlags().BoolVar(&cfg.rawJSON, "json", false, "Render raw JSON output")
	cmd.AddCommand(
		newReadCommand(deps, &cfg, "status", "Show continuous-capture health and checkpoint age", "/manager/backups/status", false),
		newCheckpointCommand(deps, &cfg),
		newSourceFenceCommand(deps, &cfg),
		newRestoreCommand(deps, &cfg),
	)
	return cmd
}

func newCheckpointCommand(deps command.Deps, cfg *config) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "checkpoint",
		Aliases: []string{"checkpoints"},
		Short:   "Inspect and publish immutable continuous-backup checkpoints",
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(
		newCheckpointListCommand(deps, cfg),
		newCheckpointShowCommand(deps, cfg),
		newCheckpointPublishCommand(deps, cfg),
		newCheckpointHoldCommand(deps, cfg, true),
		newCheckpointHoldCommand(deps, cfg, false),
	)
	return cmd
}

func newCheckpointHoldCommand(
	deps command.Deps,
	cfg *config,
	held bool,
) *cobra.Command {
	action := "hold"
	short := "Protect a checkpoint from Generation collection"
	if !held {
		action = "release"
		short = "Release a checkpoint retention hold"
	}
	return &cobra.Command{
		Use: action + " CHECKPOINT_ID", Short: short,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			checkpointID := strings.TrimSpace(args[0])
			if checkpointID == "" {
				return command.Exit{
					Code:    command.ExitConfig,
					Message: "checkpoint ID is required",
				}
			}
			return execute(
				deps, *cfg, cmd.Context(), http.MethodPost,
				"/manager/backups/checkpoints/"+
					url.PathEscape(checkpointID)+"/hold",
				map[string]any{"held": held},
			)
		},
	}
}

func newCheckpointListCommand(deps command.Deps, cfg *config) *cobra.Command {
	var cursor string
	var idQuery string
	var limit int
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List immutable catalog checkpoints",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit <= 0 || limit > 200 {
				return command.Exit{
					Code: command.ExitConfig, Message: "--limit must be between 1 and 200",
				}
			}
			values := url.Values{}
			values.Set("limit", fmt.Sprintf("%d", limit))
			if cursor = strings.TrimSpace(cursor); cursor != "" {
				values.Set("cursor", cursor)
			}
			if idQuery = strings.TrimSpace(idQuery); idQuery != "" {
				values.Set("id", idQuery)
			}
			return execute(
				deps, *cfg, cmd.Context(), http.MethodGet,
				"/manager/backups/checkpoints?"+values.Encode(), nil,
			)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum checkpoints returned by this page")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Opaque catalog page cursor")
	cmd.Flags().StringVar(&idQuery, "id", "", "Exact checkpoint ID filter")
	return cmd
}

func newCheckpointShowCommand(deps command.Deps, cfg *config) *cobra.Command {
	return &cobra.Command{
		Use:   "show CHECKPOINT_ID",
		Short: "Show one immutable checkpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			checkpointID := strings.TrimSpace(args[0])
			if checkpointID == "" {
				return command.Exit{
					Code: command.ExitConfig, Message: "checkpoint ID is required",
				}
			}
			return execute(
				deps, *cfg, cmd.Context(), http.MethodGet,
				"/manager/backups/checkpoints/"+url.PathEscape(checkpointID), nil,
			)
		},
	}
}

func newCheckpointPublishCommand(deps command.Deps, cfg *config) *cobra.Command {
	return &cobra.Command{
		Use:   "publish",
		Short: "Publish a complete vector-cut checkpoint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return execute(
				deps, *cfg, cmd.Context(), http.MethodPost,
				"/manager/backups/checkpoints", map[string]any{},
			)
		},
	}
}

func newRestoreCommand(deps command.Deps, cfg *config) *cobra.Command {
	cmd := &cobra.Command{Use: "restore", Short: "Operate explicit restore mode", Args: cobra.NoArgs}
	cmd.AddCommand(
		newReadCommand(deps, cfg, "status", "Show the current restore plan", "/manager/restore/status", false),
		newRestorePlanCommand(deps, cfg),
		newRestorePlanMutationCommand(deps, cfg, "start", "Start or resume partition installation"),
		newRestorePlanMutationCommand(deps, cfg, "verify", "Run post-install semantic verification"),
		newRestoreActivateCommand(deps, cfg),
	)
	return cmd
}

func newRestorePlanCommand(deps command.Deps, cfg *config) *cobra.Command {
	var checkpointID string
	var catalogHeadToken string
	var invalidateTokens bool
	cmd := &cobra.Command{Use: "plan", Short: "Create the immutable recovery plan", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		checkpointID = strings.TrimSpace(checkpointID)
		catalogHeadToken = strings.TrimSpace(catalogHeadToken)
		if checkpointID == "" || catalogHeadToken == "" {
			return command.Exit{Code: command.ExitConfig, Message: "--checkpoint and --catalog-head are required"}
		}
		return execute(deps, *cfg, cmd.Context(), http.MethodPost, "/manager/restore/plan", map[string]any{
			"checkpoint_id": checkpointID, "catalog_head_token": catalogHeadToken,
			"invalidate_tokens": invalidateTokens,
		})
	}}
	cmd.Flags().StringVar(&checkpointID, "checkpoint", "", "Exact immutable checkpoint ID")
	cmd.Flags().StringVar(&catalogHeadToken, "catalog-head", "", "Opaque catalog-head token observed with the checkpoint")
	cmd.Flags().BoolVar(&invalidateTokens, "invalidate-tokens", false, "Invalidate restored client tokens before activation")
	return cmd
}

func newRestorePlanMutationCommand(deps command.Deps, cfg *config, action, short string) *cobra.Command {
	return &cobra.Command{Use: action + " PLAN_ID", Short: short, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		planID := strings.TrimSpace(args[0])
		if planID == "" {
			return command.Exit{Code: command.ExitConfig, Message: "restore plan ID is required"}
		}
		return execute(deps, *cfg, cmd.Context(), http.MethodPost, "/manager/restore/"+url.PathEscape(planID)+"/"+action, map[string]any{})
	}}
}

func newRestoreActivateCommand(deps command.Deps, cfg *config) *cobra.Command {
	var receiptPath string
	var breakGlassReason string
	cmd := &cobra.Command{Use: "activate PLAN_ID", Short: "Activate only after old-cluster fencing", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		planID := strings.TrimSpace(args[0])
		receiptPath = strings.TrimSpace(receiptPath)
		breakGlassReason = strings.TrimSpace(breakGlassReason)
		if planID == "" || (receiptPath == "") == (breakGlassReason == "") {
			return command.Exit{Code: command.ExitConfig, Message: "PLAN_ID and exactly one of --source-fence-receipt or --break-glass-reason are required"}
		}
		request := map[string]any{}
		if receiptPath != "" {
			receipt, err := loadSourceFenceReceiptFile(receiptPath)
			if err != nil {
				return command.Exit{Code: command.ExitConfig, Message: err.Error()}
			}
			request["source_fence_receipt"] = receipt
		} else {
			request["break_glass"] = map[string]string{"reason": breakGlassReason}
		}
		return execute(deps, *cfg, cmd.Context(), http.MethodPost, "/manager/restore/"+url.PathEscape(planID)+"/activate", request)
	}}
	cmd.Flags().StringVar(&receiptPath, "source-fence-receipt", "", "Path to the exact signed source-fence receipt JSON")
	cmd.Flags().StringVar(&breakGlassReason, "break-glass-reason", "", "Explicit exceptional reason when the source is unrecoverable")
	return cmd
}

func newSourceFenceCommand(deps command.Deps, cfg *config) *cobra.Command {
	var restorePlanID string
	var checkpointID string
	var targetClusterID string
	var targetGeneration string
	cmd := &cobra.Command{
		Use: "fence-source", Short: "Irreversibly fence the source and issue a signed receipt",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			restorePlanID = strings.TrimSpace(restorePlanID)
			checkpointID = strings.TrimSpace(checkpointID)
			targetClusterID = strings.TrimSpace(targetClusterID)
			targetGeneration = strings.TrimSpace(targetGeneration)
			if restorePlanID == "" || checkpointID == "" ||
				targetClusterID == "" || targetGeneration == "" {
				return command.Exit{Code: command.ExitConfig, Message: "all source-fence target binding flags are required"}
			}
			return execute(
				deps, *cfg, cmd.Context(), http.MethodPost,
				"/manager/backups/source-fence",
				map[string]string{
					"restore_plan_id":   restorePlanID,
					"checkpoint_id":     checkpointID,
					"target_cluster_id": targetClusterID,
					"target_generation": targetGeneration,
				},
			)
		},
	}
	cmd.Flags().StringVar(&restorePlanID, "restore-plan", "", "Immutable successor restore plan ID")
	cmd.Flags().StringVar(&checkpointID, "checkpoint", "", "Exact immutable checkpoint ID selected by the plan")
	cmd.Flags().StringVar(&targetClusterID, "target-cluster", "", "Fresh successor cluster ID")
	cmd.Flags().StringVar(&targetGeneration, "target-generation", "", "Fresh successor generation")
	return cmd
}

func loadSourceFenceReceiptFile(path string) (backupartifact.SourceFenceReceipt, error) {
	file, err := os.Open(path)
	if err != nil {
		return backupartifact.SourceFenceReceipt{}, fmt.Errorf("open source-fence receipt: %w", err)
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return backupartifact.SourceFenceReceipt{}, fmt.Errorf("read source-fence receipt: %w", err)
	}
	if len(body) == 0 || len(body) > 64<<10 {
		return backupartifact.SourceFenceReceipt{}, fmt.Errorf("source-fence receipt exceeds the 64 KiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var receipt backupartifact.SourceFenceReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return backupartifact.SourceFenceReceipt{}, fmt.Errorf("decode source-fence receipt: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return backupartifact.SourceFenceReceipt{}, fmt.Errorf("source-fence receipt has trailing data")
	}
	return receipt, nil
}

func newReadCommand(deps command.Deps, cfg *config, use, short, path string, aliasLS bool) *cobra.Command {
	cmd := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return execute(deps, *cfg, cmd.Context(), http.MethodGet, path, nil)
	}}
	if aliasLS {
		cmd.Aliases = []string{"ls"}
	}
	return cmd
}

func execute(deps command.Deps, cfg config, ctx context.Context, method, path string, request any) error {
	server, err := resolveServer(deps, cfg)
	if err != nil {
		return command.Exit{Code: command.ExitConfig, Message: err.Error()}
	}
	body, err := call(ctx, server, cfg.token, cfg.timeout, method, path, request)
	if err != nil {
		var statusErr *statusError
		if errors.As(err, &statusErr) && statusErr.code >= 400 && statusErr.code < 500 {
			return command.Exit{Code: command.ExitConfig, Message: err.Error()}
		}
		return command.Exit{Code: command.ExitUnavailable, Message: err.Error()}
	}
	return renderResponse(deps, cfg.rawJSON, body)
}

func renderResponse(deps command.Deps, rawJSON bool, body []byte) error {
	if rawJSON {
		fmt.Fprintln(deps.Stdout, string(body))
		return nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return command.Exit{Code: command.ExitInternal, Message: fmt.Sprintf("decode Manager response: %v", err)}
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return command.Exit{Code: command.ExitInternal, Message: err.Error()}
	}
	fmt.Fprintln(deps.Stdout, string(pretty))
	return nil
}

func resolveServer(deps command.Deps, cfg config) (string, error) {
	if server := strings.TrimSpace(cfg.server); server != "" {
		return validateServer(server)
	}
	storeDir := contextcmd.DefaultStoreDir()
	if deps.ContextDir != nil && strings.TrimSpace(*deps.ContextDir) != "" {
		storeDir = *deps.ContextDir
	}
	store := contextcmd.NewStore(storeDir)
	name := strings.TrimSpace(cfg.contextName)
	if name == "" {
		var err error
		name, err = store.Current()
		if err != nil {
			return "", err
		}
	}
	if name == "" {
		return "", fmt.Errorf("--server or a selected --context is required")
	}
	saved, err := store.Load(name)
	if err != nil {
		return "", err
	}
	if len(saved.Servers) == 0 {
		return "", fmt.Errorf("context %q has no Manager server", name)
	}
	return validateServer(saved.Servers[0])
}

func validateServer(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("Manager server must be an absolute http or https URL")
	}
	return strings.TrimRight(value, "/"), nil
}

type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string { return fmt.Sprintf("Manager status %d: %s", e.code, e.body) }

func call(ctx context.Context, server, token string, timeout time.Duration, method, path string, input any) ([]byte, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, server+path, body)
	if err != nil {
		return nil, err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token = strings.TrimSpace(token); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxResponseBytes {
		return nil, fmt.Errorf("Manager response exceeds limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &statusError{code: response.StatusCode, body: strings.TrimSpace(string(payload))}
	}
	return payload, nil
}
