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
		newReadCommand(deps, &cfg, "status", "Show backup health and RPO", "/manager/backups/status", false),
		newListCommand(deps, &cfg),
		newTriggerCommand(deps, &cfg), newCancelCommand(deps, &cfg),
		newPointMutationCommand(deps, &cfg, "hold", "Place a retention hold on a restore point"),
		newPointMutationCommand(deps, &cfg, "release", "Release a restore-point retention hold"),
		newPointMutationCommand(deps, &cfg, "verify", "Verify both repository copies and the signed object chain"),
		newRestoreCommand(deps, &cfg),
	)
	return cmd
}

func newListCommand(deps command.Deps, cfg *config) *cobra.Command {
	return &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List published restore points", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executeRestorePointList(deps, *cfg, cmd.Context())
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
	var restorePointID string
	var latestVerified bool
	var repository string
	var invalidateTokens bool
	cmd := &cobra.Command{Use: "plan", Short: "Create the immutable recovery plan", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		restorePointID = strings.TrimSpace(restorePointID)
		repository = strings.TrimSpace(repository)
		if (restorePointID == "") == !latestVerified {
			return command.Exit{Code: command.ExitConfig, Message: "exactly one of --restore-point or --latest-verified is required"}
		}
		if repository != "primary" && repository != "secondary" {
			return command.Exit{Code: command.ExitConfig, Message: "--repository must be primary or secondary"}
		}
		return execute(deps, *cfg, cmd.Context(), http.MethodPost, "/manager/restore/plan", map[string]any{
			"restore_point_id": restorePointID, "latest_verified": latestVerified,
			"repository": repository, "invalidate_tokens": invalidateTokens,
		})
	}}
	cmd.Flags().StringVar(&restorePointID, "restore-point", "", "Exact signed restore-point ID")
	cmd.Flags().BoolVar(&latestVerified, "latest-verified", false, "Select the newest identical fully verified dual-repository point")
	cmd.Flags().StringVar(&repository, "repository", "primary", "Repository copy used for installation")
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
	var fenceDigest string
	cmd := &cobra.Command{Use: "activate PLAN_ID", Short: "Activate only after old-cluster fencing", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		planID := strings.TrimSpace(args[0])
		fenceDigest = strings.TrimSpace(fenceDigest)
		if planID == "" || len(fenceDigest) != 64 {
			return command.Exit{Code: command.ExitConfig, Message: "PLAN_ID and a 64-character --old-cluster-fence-digest are required"}
		}
		return execute(deps, *cfg, cmd.Context(), http.MethodPost, "/manager/restore/"+url.PathEscape(planID)+"/activate", map[string]string{
			"old_cluster_fence_digest": fenceDigest,
		})
	}}
	cmd.Flags().StringVar(&fenceDigest, "old-cluster-fence-digest", "", "SHA-256 evidence that the source cluster is fenced")
	return cmd
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

func newTriggerCommand(deps command.Deps, cfg *config) *cobra.Command {
	kind := string(backupartifact.RestorePointIncremental)
	cmd := &cobra.Command{Use: "trigger", Short: "Trigger a materialized or incremental backup", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		switch backupartifact.RestorePointKind(kind) {
		case backupartifact.RestorePointIncremental, backupartifact.RestorePointMaterializedFull:
		default:
			return command.Exit{Code: command.ExitConfig, Message: "--kind must be incremental or materialized_full; synthetic_full is not yet qualified"}
		}
		return execute(deps, *cfg, cmd.Context(), http.MethodPost, "/manager/backups/trigger", map[string]string{"kind": kind})
	}}
	cmd.Flags().StringVar(&kind, "kind", kind, "Restore-point kind")
	return cmd
}

func newCancelCommand(deps command.Deps, cfg *config) *cobra.Command {
	var epoch uint64
	cmd := &cobra.Command{Use: "cancel JOB_ID", Short: "Cancel the exact active backup epoch", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if epoch == 0 || strings.TrimSpace(args[0]) == "" {
			return command.Exit{Code: command.ExitConfig, Message: "--epoch and JOB_ID are required"}
		}
		return execute(deps, *cfg, cmd.Context(), http.MethodPost, "/manager/backups/jobs/"+url.PathEscape(args[0])+"/cancel", map[string]uint64{"epoch": epoch})
	}}
	cmd.Flags().Uint64Var(&epoch, "epoch", 0, "Exact active backup epoch")
	return cmd
}

func newPointMutationCommand(deps command.Deps, cfg *config, action, short string) *cobra.Command {
	return &cobra.Command{Use: action + " RESTORE_POINT_ID", Short: short, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return command.Exit{Code: command.ExitConfig, Message: "restore-point ID is required"}
		}
		path := "/manager/backups/restore-points/" + url.PathEscape(args[0]) + "/" + action
		return execute(deps, *cfg, cmd.Context(), http.MethodPost, path, map[string]any{})
	}}
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

func executeRestorePointList(deps command.Deps, cfg config, ctx context.Context) error {
	server, err := resolveServer(deps, cfg)
	if err != nil {
		return command.Exit{Code: command.ExitConfig, Message: err.Error()}
	}
	const maxPages = 32
	type page struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor string            `json:"next_cursor"`
		Total      int               `json:"total"`
	}
	result := page{Items: []json.RawMessage{}}
	seen := make(map[string]struct{})
	cursor := ""
	for pageIndex := 0; pageIndex < maxPages; pageIndex++ {
		path := "/manager/backups/restore-points?limit=200"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		body, callErr := call(ctx, server, cfg.token, cfg.timeout, http.MethodGet, path, nil)
		if callErr != nil {
			var statusErr *statusError
			if errors.As(callErr, &statusErr) && statusErr.code >= 400 && statusErr.code < 500 {
				return command.Exit{Code: command.ExitConfig, Message: callErr.Error()}
			}
			return command.Exit{Code: command.ExitUnavailable, Message: callErr.Error()}
		}
		var current page
		if err := json.Unmarshal(body, &current); err != nil {
			return command.Exit{Code: command.ExitInternal, Message: fmt.Sprintf("decode Manager response: %v", err)}
		}
		result.Items = append(result.Items, current.Items...)
		result.Total = current.Total
		cursor = strings.TrimSpace(current.NextCursor)
		if cursor == "" {
			result.NextCursor = ""
			combined, err := json.Marshal(result)
			if err != nil {
				return command.Exit{Code: command.ExitInternal, Message: err.Error()}
			}
			return renderResponse(deps, cfg.rawJSON, combined)
		}
		if _, exists := seen[cursor]; exists {
			return command.Exit{Code: command.ExitInternal, Message: "Manager returned a repeated restore-point cursor"}
		}
		seen[cursor] = struct{}{}
	}
	return command.Exit{Code: command.ExitInternal, Message: "restore-point pagination exceeded bounded page limit"}
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
