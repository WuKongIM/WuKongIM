package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// e2eShardCount is the number of nightly E2E shards. Serial E2E (-p=1) over
// every suite exceeded one 60-minute job, so each shard runs a stable,
// round-robin slice of the sorted package list.
const e2eShardCount = 4

// e2ePackagePrefix bounds discovery to the repository's E2E tree.
const e2ePackagePrefix = "github.com/WuKongIM/WuKongIM/test/e2e"

// checkE2EShard discovers every E2E package and runs only this shard's slice
// serially. It fails closed when discovery is empty, a listed package is
// outside test/e2e, or this shard would run nothing.
func checkE2EShard(ctx context.Context, root string, commands commandExecutor, index int) error {
	output, err := commands.Output(ctx, checkStep{
		directory: root, name: "go",
		arguments:   []string{"list", "-tags=e2e", "./test/e2e/..."},
		environment: []string{"GOWORK=off"},
	})
	if err != nil {
		return fmt.Errorf("list E2E packages: %w", err)
	}
	packages, err := e2eShardPackages(string(output), index)
	if err != nil {
		return err
	}
	arguments := append([]string{"test", "-tags=e2e"}, packages...)
	arguments = append(arguments, "-count=1", "-p=1", "-timeout=50m")
	return commands.Run(ctx, checkStep{directory: root, name: "go", arguments: arguments, environment: []string{"GOWORK=off"}})
}

// e2eShardPackages returns the sorted round-robin slice for a 1-based index.
func e2eShardPackages(listing string, index int) ([]string, error) {
	var all []string
	for _, line := range strings.Split(listing, "\n") {
		pkg := strings.TrimSpace(line)
		if pkg == "" {
			continue
		}
		if pkg != e2ePackagePrefix && !strings.HasPrefix(pkg, e2ePackagePrefix+"/") {
			return nil, fmt.Errorf("unexpected E2E package %q", pkg)
		}
		all = append(all, pkg)
	}
	slices.Sort(all)
	all = slices.Compact(all)
	if len(all) < e2eShardCount {
		return nil, errors.New("E2E discovery found fewer packages than shards")
	}
	var shard []string
	for i, pkg := range all {
		if i%e2eShardCount == index-1 {
			shard = append(shard, pkg)
		}
	}
	return shard, nil
}
