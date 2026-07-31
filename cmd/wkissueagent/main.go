// Command wkissueagent runs the JSON-only GitHub Issue Agent v2 boundary.
package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
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
	workingDirectory, _ := os.Getwd()
	apiBaseURL := os.Getenv("GITHUB_API_URL")
	if apiBaseURL == "" {
		apiBaseURL = "https://api.github.com"
	}
	config := app.IssueAgentConfig{
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
		APIBaseURL:  apiBaseURL,
		Repository:  os.Getenv("GITHUB_REPOSITORY"),
		GitHubToken: os.Getenv("ISSUE_AGENT_GITHUB_TOKEN"),
		AppLogin:    os.Getenv("ISSUE_AGENT_APP_LOGIN"),
		AppID:       parsePositiveInt64(os.Getenv("ISSUE_AGENT_APP_ID")),
		AppInstallationID: parsePositiveInt64(
			os.Getenv("ISSUE_AGENT_APP_INSTALLATION_ID"),
		),
		RepositoryID: parsePositiveInt64(
			os.Getenv("ISSUE_AGENT_REPOSITORY_ID"),
		),
		AppPrivateKeyPEM: []byte(
			os.Getenv("ISSUE_AGENT_APP_PRIVATE_KEY"),
		),
		ReviewAgentAppLogin: os.Getenv("REVIEW_AGENT_APP_LOGIN"),
		WorkingDirectory:    workingDirectory,
		Now:                 time.Now,
	}
	return issueagentcli.Run(
		ctx,
		args,
		stdin,
		stdout,
		stderr,
		app.NewIssueAgentOperations(config),
	)
}

func parsePositiveInt64(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}
