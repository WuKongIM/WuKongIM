package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeCommandExecutor struct {
	outputs      []checkStep
	runs         []checkStep
	output       func(context.Context, checkStep) ([]byte, error)
	run          func(context.Context, checkStep) error
	revision     string
	lockSHA      string
	cancelOnRun  int
	cancel       context.CancelFunc
	unexpectedOK bool
}

func (executor *fakeCommandExecutor) Output(
	ctx context.Context,
	step checkStep,
) ([]byte, error) {
	executor.outputs = append(executor.outputs, cloneCheckStep(step))
	if executor.output != nil {
		return executor.output(ctx, step)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch {
	case step.name == "git" && slices.Contains(step.arguments, "ls-files"):
		return []byte("cmd/wkcicheck/main.go\x00cmd/wkcicheck/docs_integration.go\x00"), nil
	case step.name == "gofmt":
		return nil, nil
	case step.name == "bun" && slices.Equal(step.arguments, []string{"--version"}):
		return []byte("1.3.11\n"), nil
	case step.name == "node" && slices.Equal(step.arguments, []string{"--version"}):
		return []byte("v22.12.0\n"), nil
	case step.name == "yarn" && slices.Equal(step.arguments, []string{"--version"}):
		return []byte("1.22.22\n"), nil
	case step.name == "git" && slices.Contains(step.arguments, "rev-parse"):
		return []byte(executor.revision + "\n"), nil
	case step.name == "git" && slices.Contains(step.arguments, "status"):
		return nil, nil
	case executor.unexpectedOK:
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected output command: %s %v", step.name, step.arguments)
	}
}

func (executor *fakeCommandExecutor) Run(
	ctx context.Context,
	step checkStep,
) error {
	executor.runs = append(executor.runs, cloneCheckStep(step))
	if executor.run != nil {
		return executor.run(ctx, step)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, entry := range step.environment {
		const prefix = "WK_DOCS_GOLDEN_PATH_ATTESTATION_OUTPUT="
		if strings.HasPrefix(entry, prefix) {
			path := strings.TrimPrefix(entry, prefix)
			if path == "" {
				continue
			}
			return os.WriteFile(
				path,
				[]byte(goldenPathReceiptFixture(executor.revision, executor.lockSHA)),
				0o600,
			)
		}
	}
	if executor.cancelOnRun > 0 && len(executor.runs) == executor.cancelOnRun {
		executor.cancel()
	}
	return nil
}

func cloneCheckStep(step checkStep) checkStep {
	step.arguments = append([]string(nil), step.arguments...)
	step.environment = append([]string(nil), step.environment...)
	return step
}

func TestEverySelectorRunsOnlyPinnedTools(t *testing.T) {
	selectors := slices.Clone(Selectors)
	slices.Sort(selectors)
	require.NotEmpty(t, selectors)

	root := t.TempDir()
	lockfile := []byte(`{"lockfileVersion":3}`)
	sampleRoot := filepath.Join(root, "docs-site", "examples", "javascript-web-quickstart")
	require.NoError(t, os.MkdirAll(sampleRoot, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sampleRoot, "package-lock.json"), lockfile, 0o600))
	lockHash := sha256.Sum256(lockfile)

	for _, selector := range selectors {
		selector := selector
		t.Run(selector, func(t *testing.T) {
			executor := &fakeCommandExecutor{
				revision: strings.Repeat("a", 40),
				lockSHA:  hex.EncodeToString(lockHash[:]),
			}
			require.NoError(t, runCICheck(context.Background(), root, selector, executor))
			require.NotEmpty(t, append(executor.outputs, executor.runs...))

			for _, step := range append(executor.outputs, executor.runs...) {
				require.Contains(t, []string{
					"git", "gofmt", "go", "bun", "bunx", "node", "npm", "yarn", "bash",
				}, step.name)
			}
		})
	}
}

func TestThreeNodeSelectorKeepsTheFixedFailSafeScenario(t *testing.T) {
	t.Parallel()

	executor := &fakeCommandExecutor{}
	require.NoError(t, runCICheck(
		context.Background(), "/workspace", "three-node", executor,
	))
	require.Equal(t, []checkStep{{
		directory: "/workspace",
		name:      "bash",
		arguments: []string{
			"scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
			"--out-dir", ".ci-output/three-node-smoke",
			"--ready-timeout", "180",
		},
		environment: []string{
			"WK_WUKONGIM_THREE_NODES_PROMETHEUS_ENABLE=false",
			"WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE=false",
			"WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_CONTROLLER_VOTER=false",
			"WK_WKCLI_SIM_THREE_SMOKE_FAULT_KILL_NODE=false",
		},
	}}, executor.runs)
}

func TestCICheckCancellationStopsBeforeAndBetweenCommands(t *testing.T) {
	t.Parallel()

	t.Run("before selector", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		executor := &fakeCommandExecutor{}
		err := runCICheck(ctx, "/workspace", "go-mod-tidy", executor)
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, executor.outputs)
		require.Empty(t, executor.runs)
	})

	t.Run("between plan steps", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		executor := &fakeCommandExecutor{cancelOnRun: 1, cancel: cancel}
		err := runCICheck(ctx, "/workspace", "docs", executor)
		require.ErrorIs(t, err, context.Canceled)
		require.Len(t, executor.outputs, 1, "the pinned tool version is checked first")
		require.Len(t, executor.runs, 1, "no command after cancellation may start")
	})
}

func TestCICheckClassifiesToolAndArtifactFailures(t *testing.T) {
	t.Parallel()

	t.Run("pinned version mismatch", func(t *testing.T) {
		t.Parallel()
		executor := &fakeCommandExecutor{output: func(_ context.Context, _ checkStep) ([]byte, error) {
			return []byte("1.4.0\n"), nil
		}}
		err := runCICheck(context.Background(), "/workspace", "docs", executor)
		require.EqualError(t, err, "unexpected CI check tool bun version")
		require.Empty(t, executor.runs)
	})

	t.Run("failed command does not leak implementation error", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("sensitive process detail")
		executor := &fakeCommandExecutor{
			revision: strings.Repeat("a", 40),
			run: func(_ context.Context, _ checkStep) error {
				return sentinel
			},
		}
		err := runCICheck(context.Background(), "/workspace", "go-mod-tidy", executor)
		require.EqualError(t, err, "CI check command go failed")
		require.NotErrorIs(t, err, sentinel)
	})

	t.Run("three-node failure has scenario classification", func(t *testing.T) {
		t.Parallel()
		executor := &fakeCommandExecutor{run: func(_ context.Context, _ checkStep) error {
			return errors.New("failed")
		}}
		err := runCICheck(context.Background(), "/workspace", "three-node", executor)
		require.EqualError(t, err, "three-node cluster smoke failed")
	})

	t.Run("stale generated bundle blocks publication", func(t *testing.T) {
		t.Parallel()
		executor := &fakeCommandExecutor{output: func(_ context.Context, step checkStep) ([]byte, error) {
			switch {
			case step.name == "bun":
				return []byte("1.3.11\n"), nil
			case step.name == "git":
				return []byte(" M internal/access/manager/webui/dist/app.js\n"), nil
			default:
				return nil, fmt.Errorf("unexpected output command %s", step.name)
			}
		}}
		err := runCICheck(context.Background(), "/workspace", "web", executor)
		require.EqualError(t, err, "generated embedded bundle is stale")
	})
}

func TestGoFormatSelectorValidatesTheTrackedInventory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		outputs   [][]byte
		errors    []error
		expected  string
		callCount int
	}{
		{
			name: "git inventory failure", errors: []error{errors.New("git failed")},
			expected: "list tracked Go files", callCount: 1,
		},
		{
			name: "empty inventory", outputs: [][]byte{nil},
			expected: "tracked Go file inventory is empty", callCount: 1,
		},
		{
			name: "gofmt failure", outputs: [][]byte{[]byte("main.go\x00")},
			errors:   []error{nil, errors.New("gofmt failed")},
			expected: "run gofmt inventory", callCount: 2,
		},
		{
			name: "unformatted inventory", outputs: [][]byte{[]byte("main.go\x00"), []byte("main.go\n")},
			expected: "tracked Go files require gofmt", callCount: 2,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			call := 0
			executor := &fakeCommandExecutor{output: func(_ context.Context, _ checkStep) ([]byte, error) {
				index := call
				call++
				var output []byte
				if index < len(test.outputs) {
					output = test.outputs[index]
				}
				var err error
				if index < len(test.errors) {
					err = test.errors[index]
				}
				return output, err
			}}
			err := runCICheck(context.Background(), "/workspace", "go-format", executor)
			require.EqualError(t, err, test.expected)
			require.Equal(t, test.callCount, call)
		})
	}
}

func TestCheckExecCommandBuildsAnExplicitScopedEnvironment(t *testing.T) {
	t.Setenv("WK_REVIEW_CHECK_BOUNDARY", "ambient")

	command := checkExecCommand(context.Background(), checkStep{
		directory: "/workspace/docs-site",
		name:      "bun",
		arguments: []string{"run", "verify"},
		environment: []string{
			"WK_REVIEW_CHECK_BOUNDARY=trusted",
			"WK_REVIEW_CHECK_EMPTY=",
		},
	})
	require.Equal(t, "/workspace/docs-site", command.Dir)
	require.Equal(t, []string{"bun", "run", "verify"}, command.Args)
	require.Nil(t, command.Stdin)
	require.Equal(t, 1, countEnvironmentKey(command.Env, "WK_REVIEW_CHECK_BOUNDARY"))
	require.Contains(t, command.Env, "WK_REVIEW_CHECK_BOUNDARY=trusted")
	require.Contains(t, command.Env, "WK_REVIEW_CHECK_EMPTY=")

	inherited := checkExecCommand(context.Background(), checkStep{name: "go"})
	require.Nil(t, inherited.Env, "nil preserves the subprocess inherited environment")
}

func countEnvironmentKey(environment []string, key string) int {
	count := 0
	for _, entry := range environment {
		if environmentKey(entry) == key {
			count++
		}
	}
	return count
}
