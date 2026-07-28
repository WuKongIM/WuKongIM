// Command wkissueagent runs the standalone GitHub Actions Issue Agent.
package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	"github.com/WuKongIM/WuKongIM/internal/app"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer cancel()
	dependencies := app.NewIssueAgentGitHubDependencies(app.IssueAgentGitHubConfig{
		HTTPClient:      &http.Client{Timeout: 30 * time.Second},
		GitHubToken:     os.Getenv("ISSUE_AGENT_GITHUB_TOKEN"),
		CheckpointKeyID: os.Getenv("ISSUE_AGENT_CHECKPOINT_KEY_ID"),
		CheckpointPrivateKeyBase64: os.Getenv(
			"ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY",
		),
		AppPrivateKeyPEM: []byte(os.Getenv("ISSUE_AGENT_APP_PRIVATE_KEY")),
		Now:              time.Now,
	})
	return issueagentcli.Run(
		ctx, args, stdin, stdout, stderr,
		app.NewIssueAgentOperations(dependencies),
	)
}
