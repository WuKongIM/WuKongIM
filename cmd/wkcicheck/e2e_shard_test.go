package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// e2eShardListing answers only the package-discovery command; any other
// output command is a contract violation.
func e2eShardListing(packages []string) func(context.Context, checkStep) ([]byte, error) {
	return func(_ context.Context, step checkStep) ([]byte, error) {
		if step.name == "go" && slices.Equal(step.arguments, []string{"list", "-tags=e2e", "./test/e2e/..."}) {
			return []byte(strings.Join(packages, "\n") + "\n"), nil
		}
		return nil, fmt.Errorf("unexpected output command: %s %v", step.name, step.arguments)
	}
}

// Nightly E2E is split so that every suite finishes inside one job. The
// shards must cover every package exactly once, independent of listing order.
func TestE2EShardsCoverEveryPackageExactlyOnce(t *testing.T) {
	packages := []string{
		"github.com/WuKongIM/WuKongIM/test/e2e/mqtt/will",
		"github.com/WuKongIM/WuKongIM/test/e2e/cluster/a",
		"github.com/WuKongIM/WuKongIM/test/e2e/message/b",
		"github.com/WuKongIM/WuKongIM/test/e2e/mqtt/session",
		"github.com/WuKongIM/WuKongIM/test/e2e/suite",
		"github.com/WuKongIM/WuKongIM/test/e2e/backup/c",
		"github.com/WuKongIM/WuKongIM/test/e2e/plugin",
		"github.com/WuKongIM/WuKongIM/test/e2e/migration/d",
		"github.com/WuKongIM/WuKongIM/test/e2e/opsmcp/e",
	}
	run := func(listing []string) map[string][]string {
		got := map[string][]string{}
		for i := 1; i <= e2eShardCount; i++ {
			selector := fmt.Sprintf("e2e-shard-%d", i)
			executor := &fakeCommandExecutor{output: e2eShardListing(listing)}
			require.NoError(t, runCICheck(context.Background(), t.TempDir(), selector, executor))
			require.Len(t, executor.runs, 1, selector)
			step := executor.runs[0]
			require.Equal(t, "go", step.name)
			require.Contains(t, step.environment, "GOWORK=off")
			require.Equal(t, []string{"test", "-tags=e2e"}, step.arguments[:2])
			for _, flag := range []string{"-count=1", "-p=1", "-timeout=50m"} {
				require.Contains(t, step.arguments, flag, selector)
			}
			for _, argument := range step.arguments {
				if strings.HasPrefix(argument, "github.com/") {
					got[selector] = append(got[selector], argument)
				}
			}
			require.NotEmpty(t, got[selector], selector)
		}
		return got
	}

	shards := run(packages)
	var covered []string
	for _, shard := range shards {
		covered = append(covered, shard...)
	}
	slices.Sort(covered)
	want := slices.Clone(packages)
	slices.Sort(want)
	require.Equal(t, want, covered, "every package exactly once")

	reversed := slices.Clone(packages)
	slices.Reverse(reversed)
	require.Equal(t, shards, run(reversed), "assignment must not depend on listing order")
	for i := 1; i <= e2eShardCount; i++ {
		require.Contains(t, Selectors, fmt.Sprintf("e2e-shard-%d", i))
	}
	require.NotContains(t, Selectors, fmt.Sprintf("e2e-shard-%d", e2eShardCount+1), "selectors must match the shard count")
}

// A broken discovery or a shard with nothing to run must fail, never pass.
func TestE2EShardFailsClosed(t *testing.T) {
	for name, listing := range map[string][]string{
		"no packages":       nil,
		"fewer than shards": {"github.com/WuKongIM/WuKongIM/test/e2e/only"},
	} {
		t.Run(name, func(t *testing.T) {
			executor := &fakeCommandExecutor{output: e2eShardListing(listing)}
			err := runCICheck(context.Background(), t.TempDir(), fmt.Sprintf("e2e-shard-%d", e2eShardCount), executor)
			require.Error(t, err)
			require.Empty(t, executor.runs, "no test may run on a broken plan")
		})
	}
	executor := &fakeCommandExecutor{output: e2eShardListing([]string{"not-a-repository-package"})}
	require.Error(t, runCICheck(context.Background(), t.TempDir(), "e2e-shard-1", executor))
	require.Error(t, runCICheck(context.Background(), t.TempDir(), "e2e-shard-0", &fakeCommandExecutor{}))
}
