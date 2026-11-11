package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	// gofailMarker identifies a failpoint declaration that gofail rewrites.
	gofailMarker = "// gofail: var "
	// gofailAlwaysEnabledPackage is enabled by scripts/build-gofail-binary.sh
	// itself; passing it again would rewrite the same sources twice.
	gofailAlwaysEnabledPackage = "pkg/transport"
	// gofailOutputDir holds the binary and bounded evidence for upload.
	gofailOutputDir = ".ci-output/gofail-e2e"
)

// gofailExcludedSuitePrefixes are gofail E2E trees deliberately left out of
// the nightly run. MQTT is a default-off preview whose 12 gofail packages
// document 5-65 minute budgets each, beyond one nightly job; run them by hand
// with the commands in test/e2e/AGENTS.md until MQTT leaves preview.
var gofailExcludedSuitePrefixes = []string{"test/e2e/mqtt/"}

// gofailGatePattern matches the opt-in variables that gofail scenarios
// compare with "1". Every discovered gate is enabled so no suite skips.
var gofailGatePattern = regexp.MustCompile(`WK_E2E_GOFAIL_[A-Z0-9_]+`)

// checkGofailE2E builds one failpoint-enabled server and runs every
// gofail-gated E2E package outside gofailExcludedSuitePrefixes against it. Discovery keeps new scenarios covered
// without editing the workflow.
func checkGofailE2E(ctx context.Context, root string, commands commandExecutor) error {
	steps, err := gofailE2ESteps(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(gofailOutputDir)), 0o755); err != nil {
		return errors.New("create gofail E2E output directory")
	}
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("CI check canceled: %w", err)
		}
		if err := commands.Run(ctx, step); err != nil {
			if step.name == "bash" {
				return errors.New("gofail binary build failed")
			}
			return errors.New("gofail E2E suites failed")
		}
	}
	return nil
}

// gofailE2ESteps returns the build-then-test plan, failing closed when
// discovery finds no failpoint package or no gated suite.
func gofailE2ESteps(root string) ([]checkStep, error) {
	packages, err := discoverFailpointPackages(root)
	if err != nil {
		return nil, err
	}
	if len(packages) == 0 {
		return nil, errors.New("no failpoint packages discovered")
	}
	suites, gates, err := discoverGofailSuites(root)
	if err != nil {
		return nil, err
	}
	if len(suites) == 0 {
		return nil, errors.New("no gofail E2E suites discovered")
	}
	out := filepath.Join(root, filepath.FromSlash(gofailOutputDir))
	binary := filepath.Join(out, "wukongim-gofail")

	build := []string{"scripts/build-gofail-binary.sh", "--cmd", "./cmd/wukongim"}
	for _, pkg := range packages {
		build = append(build, "--package", pkg)
	}
	build = append(build, "--out", binary)

	test := append([]string{"test", "-tags=e2e"}, suites...)
	test = append(test, "-count=1", "-p=1", "-timeout=50m")
	environment := []string{"GOWORK=off", "WK_E2E_BINARY=" + binary}
	for _, gate := range gates {
		environment = append(environment, gate+"=1")
	}
	// The Issue #1077 scenario writes its repeatable JSON artifact here.
	environment = append(environment, "WK_E2E_REPLICA_REPLACE_FAILOVER_REPORT="+filepath.Join(out, "replica-replace-failover.json"))

	return []checkStep{
		{directory: root, name: "bash", arguments: build, environment: []string{"GOWORK=off"}},
		{directory: root, name: "go", arguments: test, environment: environment},
	}, nil
}

// discoverFailpointPackages lists repository-relative pkg/ and internal/
// directories whose non-test Go sources declare a failpoint.
func discoverFailpointPackages(root string) ([]string, error) {
	var packages []string
	for _, base := range []string{"internal", "pkg"} {
		err := walkGoFiles(root, base, func(rel string, test bool, content string) {
			if test || !strings.Contains(content, gofailMarker) {
				return
			}
			dir := filepath.ToSlash(filepath.Dir(rel))
			if dir != gofailAlwaysEnabledPackage && !slices.Contains(packages, dir) {
				packages = append(packages, dir)
			}
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(packages)
	return packages, nil
}

// discoverGofailSuites lists E2E scenario packages whose tests reference a
// gofail gate, plus the gates themselves. Harness self-tests are excluded.
func discoverGofailSuites(root string) ([]string, []string, error) {
	var suites, gates []string
	err := walkGoFiles(root, "test/e2e", func(rel string, test bool, content string) {
		dir := filepath.ToSlash(filepath.Dir(rel))
		if !test || dir == "test/e2e/suite" || strings.HasPrefix(dir, "test/e2e/suite/") {
			return
		}
		for _, prefix := range gofailExcludedSuitePrefixes {
			if strings.HasPrefix(dir+"/", prefix) {
				return
			}
		}
		found := gofailGatePattern.FindAllString(content, -1)
		if len(found) == 0 {
			return
		}
		if suite := "./" + dir; !slices.Contains(suites, suite) {
			suites = append(suites, suite)
		}
		for _, gate := range found {
			if !slices.Contains(gates, gate) {
				gates = append(gates, gate)
			}
		}
	})
	if err != nil {
		return nil, nil, err
	}
	slices.Sort(suites)
	slices.Sort(gates)
	return suites, gates, nil
}

// walkGoFiles visits Go files below root/base, skipping testdata and hidden
// directories. A missing base directory contributes nothing.
func walkGoFiles(root, base string, visit func(rel string, test bool, content string)) error {
	start := filepath.Join(root, filepath.FromSlash(base))
	if _, err := os.Stat(start); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(start, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("scan %s: %w", base, err)
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != start && (name == "testdata" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		visit(rel, strings.HasSuffix(entry.Name(), "_test.go"), string(content))
		return nil
	})
}
