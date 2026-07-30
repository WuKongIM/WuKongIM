// Command wkreviewcheckmcp serves the credential-free named Check MCP.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	checkmcp "github.com/WuKongIM/WuKongIM/internal/access/reviewagentcheckmcp"
	"github.com/WuKongIM/WuKongIM/internal/app"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	verify "github.com/WuKongIM/WuKongIM/internal/runtime/reviewagentverify"
)

func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		_, _ = os.Stderr.WriteString("review check MCP failed\n")
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if ctx == nil || len(args) != 0 {
		return errors.New("Review Check MCP arguments are invalid")
	}
	contextFile, err := os.Open(os.Getenv("REVIEW_CONTEXT_PATH"))
	if err != nil {
		return errors.New("open Review Check context")
	}
	defer contextFile.Close()
	contextDocument, err := contract.DecodeReviewContext(
		contextFile,
		128<<20,
	)
	if err != nil {
		return err
	}
	policy, _, err := app.LoadReviewAgentPolicy(
		os.Getenv("REVIEW_POLICY_PATH"),
	)
	if err != nil {
		return err
	}
	ledger, err := verify.NewFileLedger(
		os.Getenv("REVIEW_EVIDENCE_LEDGER"),
		os.Getenv("REVIEW_WORKSPACE"),
	)
	if err != nil {
		return err
	}
	executor, err := verify.NewOSExecutor(verify.OSExecutorConfig{
		HomeDir:       os.Getenv("REVIEW_AGENT_HOME"),
		Path:          os.Getenv("PATH"),
		TempDir:       filepath.Join(os.Getenv("REVIEW_AGENT_HOME"), "tmp"),
		WorkspaceRoot: os.Getenv("REVIEW_WORKSPACE"),
		SandboxBinary: "/usr/bin/bwrap",
		HelperBinary:  filepath.Join(os.Getenv("RUNNER_TEMP"), "wkreviewcheck"),
	})
	if err != nil {
		return err
	}
	runner, err := verify.NewRunner(verify.RunnerConfig{
		WorkspaceRoot: os.Getenv("REVIEW_WORKSPACE"),
		Policy:        policy.VerificationPolicy(),
		Executor:      executor,
		Ledger:        ledger,
	})
	if err != nil {
		return err
	}
	return checkmcp.RunStdio(ctx, checkmcp.Config{
		Runner: runner, Generation: contextDocument.Generation,
	})
}
