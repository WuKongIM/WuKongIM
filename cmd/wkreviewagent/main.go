// Command wkreviewagent runs the strict JSON-only Review Agent boundary.
package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	cli "github.com/WuKongIM/WuKongIM/internal/access/reviewagentcli"
	"github.com/WuKongIM/WuKongIM/internal/app"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) int {
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()
	config := reviewAgentConfig(args)
	return cli.Run(
		ctx,
		args,
		stdin,
		stdout,
		stderr,
		app.NewReviewAgentOperations(config),
	)
}

func reviewAgentConfig(args []string) app.ReviewAgentConfig {
	workingDirectory, _ := os.Getwd()
	apiURL := environmentOr("GITHUB_API_URL", "https://api.github.com")
	graphqlURL := environmentOr(
		"GITHUB_GRAPHQL_URL",
		"https://api.github.com/graphql",
	)
	config := app.ReviewAgentConfig{
		HTTPClient:      &http.Client{Timeout: 30 * time.Second},
		APIBaseURL:      apiURL,
		GraphQLURL:      graphqlURL,
		Repository:      os.Getenv("GITHUB_REPOSITORY"),
		GitHubReadToken: os.Getenv("REVIEW_AGENT_READ_TOKEN"),
		ControlSHA:      os.Getenv("REVIEW_AGENT_CONTROL_SHA"),
		PolicyPath: environmentOr(
			"REVIEW_POLICY_PATH",
			filepath.Join(
				workingDirectory,
				".github",
				"review-agent",
				"policy.json",
			),
		),
		PromptPath:         filepath.Join(workingDirectory, ".github", "review-agent", "prompts", "review.md"),
		ResultSchemaPath:   filepath.Join(workingDirectory, ".github", "review-agent", "review-result.schema.json"),
		WorkspaceDirectory: workingDirectory,
		EvidenceLedgerPath: filepath.Join(os.Getenv("RUNNER_TEMP"), "review-agent-evidence", "ledger.jsonl"),
		ExecutorHome:       os.Getenv("REVIEW_AGENT_HOME"),
		ExecutablePath:     os.Getenv("PATH"),
		TemporaryDirectory: filepath.Join(
			os.Getenv("REVIEW_AGENT_HOME"),
			"tmp",
		),
		ProcessSandboxPath: "/usr/bin/bwrap",
		ProcessHelperPath: filepath.Join(
			os.Getenv("RUNNER_TEMP"),
			"wkreviewcheck",
		),
		Now: func() time.Time { return time.Now().UTC() },
	}
	if len(args) != 1 {
		return config
	}
	switch args[0] {
	case "append-state":
		config.StateWriterApp = appConfigFromEnvironment(
			"REVIEW_STATE_WRITER",
		)
	case "publish-review":
		config.ReviewApp = appConfigFromEnvironment("REVIEW_AGENT")
	}
	return config
}

func appConfigFromEnvironment(prefix string) *app.ReviewAgentAppConfig {
	return &app.ReviewAgentAppConfig{
		AppID: parsePositiveInt64(os.Getenv(prefix + "_APP_ID")),
		InstallationID: parsePositiveInt64(
			os.Getenv(prefix + "_APP_INSTALLATION_ID"),
		),
		RepositoryID: parsePositiveInt64(
			os.Getenv(prefix + "_REPOSITORY_ID"),
		),
		PrivateKeyPEM: []byte(os.Getenv(prefix + "_APP_PRIVATE_KEY")),
	}
}

func parsePositiveInt64(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func environmentOr(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
