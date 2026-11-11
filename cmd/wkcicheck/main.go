// Command wkcicheck is the single source of named CI checks. GitHub Actions
// and local development invoke the same selector, never an arbitrary command:
//
//	go run ./cmd/wkcicheck <selector>
//
// Selectors is the complete catalog; .github/workflows/ci.yml references these
// names instead of copying their commands.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New("CI check selector is required")
	}
	root, err := os.Getwd()
	if err != nil {
		return errors.New("resolve CI check workspace")
	}
	return runCICheck(
		context.Background(), root, args[0], execCommandExecutor{},
	)
}

// Selectors lists every named check accepted by wkcicheck.
var Selectors = []string{
	"webhook-warm-diagnostic",
	"go-format", "go-vet", "go-mod-tidy",
	"flow-doc-contracts", "agent-artifact-contracts", "skill-focused-contracts",
	"web", "demo", "docs", "docs-integration", "integration", "three-node", "gofail-e2e",
	"e2e-shard-1", "e2e-shard-2", "e2e-shard-3", "e2e-shard-4",
}

func runCICheck(
	ctx context.Context,
	root string,
	selector string,
	commands commandExecutor,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("CI check canceled: %w", err)
	}
	switch selector {
	case "webhook-warm-diagnostic":
		binary := filepath.Join(os.TempDir(), "wukongim-webhook-diagnostic")
		if err := runCommandWithEnvironment(ctx, commands, root, []string{"GOWORK=off"}, "go", "build", "-buildvcs=false", "-o", binary, "./cmd/wukongim"); err != nil {
			return err
		}
		return runCommandWithEnvironment(ctx, commands, root, []string{"GOWORK=off", "WK_E2E_BINARY=" + binary}, "go", "test", "-tags=e2e", "./test/e2e/message/webhook", "-run", "^TestBeforeSendWebhookAuthenticatedFaults$", "-count=20", "-failfast", "-timeout=15m", "-p=1", "-v")

	case "go-vet":
		return runCommandWithEnvironment(ctx, commands, root, []string{"GOWORK=off"},
			"go", "vet", "./cmd/...", "./internal/...", "./pkg/...", "./scripts/...", "./docker/...")
	case "flow-doc-contracts":
		return runCommandWithEnvironment(ctx, commands, root, []string{"GOWORK=off"},
			"go", "run", "./scripts/flowcheck", "--mode", "check")
	case "agent-artifact-contracts":
		return runCommandWithEnvironment(ctx, commands, root, []string{"GOWORK=off"},
			"go", "run", "./scripts/skillcheck")
	case "skill-focused-contracts":
		return runCommandWithEnvironment(ctx, commands, root, []string{"GOWORK=off"},
			"go", "run", "./scripts/skillcheck", "--run-focused")
	case "go-format":
		return checkGoFormat(ctx, root, commands)
	case "go-mod-tidy":
		return runCommand(ctx, commands, root, "go", "mod", "tidy", "-diff")
	case "web":
		return checkWeb(ctx, root, commands)
	case "demo":
		return checkDemo(ctx, root, commands)
	case "docs":
		return checkDocumentation(ctx, root, commands)
	case "docs-integration":
		return checkDocumentationIntegration(ctx, root, commands)
	case "integration":
		// Real clusters and fixed latency gates must not compete with unrelated
		// package test processes for the same runner's CPU, disk and sockets.
		return runCommandWithEnvironment(ctx, commands, root, []string{"GOWORK=off"},
			"go", "test", "-tags=integration", "./internal/...", "./pkg/...", "-count=1", "-p=1", "-timeout=50m")
	case "three-node":
		return checkThreeNode(ctx, root, commands)
	case "gofail-e2e":
		return checkGofailE2E(ctx, root, commands)
	case "e2e-shard-1":
		return checkE2EShard(ctx, root, commands, 1)
	case "e2e-shard-2":
		return checkE2EShard(ctx, root, commands, 2)
	case "e2e-shard-3":
		return checkE2EShard(ctx, root, commands, 3)
	case "e2e-shard-4":
		return checkE2EShard(ctx, root, commands, 4)
	default:
		return errors.New("unknown CI check selector")
	}
}

type checkStep struct {
	directory   string
	name        string
	arguments   []string
	environment []string
}

type commandExecutor interface {
	Output(context.Context, checkStep) ([]byte, error)
	Run(context.Context, checkStep) error
}

type execCommandExecutor struct{}

func (execCommandExecutor) Output(
	ctx context.Context,
	step checkStep,
) ([]byte, error) {
	command := checkExecCommand(ctx, step)
	command.Stderr = os.Stderr
	return command.Output()
}

func (execCommandExecutor) Run(
	ctx context.Context,
	step checkStep,
) error {
	command := checkExecCommand(ctx, step)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func checkExecCommand(ctx context.Context, step checkStep) *exec.Cmd {
	command := exec.CommandContext(ctx, step.name, step.arguments...)
	command.Dir = step.directory
	command.Stdin = nil
	if len(step.environment) > 0 {
		command.Env = mergeEnvironment(os.Environ(), step.environment)
	}
	return command
}

type documentationIntegrationPlan struct {
	beforeReceipt []checkStep
	afterReceipt  []checkStep
}

func documentationCheckSteps(root string) []checkStep {
	return []checkStep{
		{
			directory: root,
			name:      "go",
			arguments: []string{
				"test", "./scripts/...", "-run", "Docs|Markdown|Link", "-count=1",
			},
			environment: []string{"GOWORK=off"},
		},
		{
			directory: filepath.Join(root, "docs-site"),
			name:      "bun",
			arguments: []string{"install", "--frozen-lockfile"},
		},
		{
			directory: filepath.Join(root, "docs-site"),
			name:      "bun",
			arguments: []string{"run", "verify"},
		},
	}
}

func documentationIntegrationCheckPlan(root, receiptPath string) documentationIntegrationPlan {
	docsRoot := filepath.Join(root, "docs-site")
	sampleRoot := filepath.Join(root, "docs-site", "examples", "javascript-web-quickstart")
	unverifiedEnvironment := []string{
		"WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH=",
		"WK_DOCS_GOLDEN_PATH_RECEIPT_JSON=",
		"WK_DOCS_REQUIRE_VERIFIED=",
		"WK_DOCS_SOURCE_REVISION=",
	}
	verifiedEnvironment := []string{
		"WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH=" + receiptPath,
		"WK_DOCS_GOLDEN_PATH_RECEIPT_JSON=",
		"WK_DOCS_REQUIRE_VERIFIED=1",
		"WK_DOCS_SOURCE_REVISION=",
	}
	return documentationIntegrationPlan{
		beforeReceipt: []checkStep{
			{
				directory: docsRoot,
				name:      "bun",
				arguments: []string{"install", "--frozen-lockfile"},
			},
			{
				directory: sampleRoot,
				name:      "npm",
				arguments: []string{"ci"},
			},
			{
				directory:   docsRoot,
				name:        "bun",
				arguments:   []string{"run", "test"},
				environment: unverifiedEnvironment,
			},
			{
				directory:   docsRoot,
				name:        "bun",
				arguments:   []string{"run", "build"},
				environment: unverifiedEnvironment,
			},
			{
				directory:   docsRoot,
				name:        "bun",
				arguments:   []string{"run", "test:output"},
				environment: unverifiedEnvironment,
			},
			{
				directory: sampleRoot,
				name:      "npm",
				arguments: []string{"exec", "--", "playwright", "install", "chromium"},
			},
			{
				directory: root,
				name:      "go",
				arguments: []string{
					"test", "-tags=e2e",
					"./test/e2e/message/javascript_web_quickstart",
					"-count=1", "-timeout=10m", "-p=1",
				},
				environment: []string{
					"GOWORK=off",
					"WK_E2E_DOCS_JAVASCRIPT_WEB=1",
					"WK_DOCS_GOLDEN_PATH_ATTESTATION_OUTPUT=" + receiptPath,
				},
			},
		},
		afterReceipt: []checkStep{
			{
				directory:   docsRoot,
				name:        "bun",
				arguments:   []string{"run", "build"},
				environment: verifiedEnvironment,
			},
			{
				directory:   docsRoot,
				name:        "bun",
				arguments:   []string{"run", "test:output"},
				environment: verifiedEnvironment,
			},
		},
	}
}

func checkDocumentation(
	ctx context.Context,
	root string,
	commands commandExecutor,
) error {
	docsRoot := filepath.Join(root, "docs-site")
	if err := requireVersion(
		ctx, commands, docsRoot, "bun", "1.3.11", "--version",
	); err != nil {
		return err
	}
	return runCheckSteps(ctx, commands, documentationCheckSteps(root))
}

func checkDocumentationIntegration(
	ctx context.Context,
	root string,
	commands commandExecutor,
) (resultErr error) {
	docsRoot := filepath.Join(root, "docs-site")
	sampleRoot := filepath.Join(root, "docs-site", "examples", "javascript-web-quickstart")
	if err := requireVersion(
		ctx, commands, docsRoot, "bun", "1.3.11", "--version",
	); err != nil {
		return err
	}
	if err := requireVersion(
		ctx, commands, sampleRoot, "node", "v22.12.0", "--version",
	); err != nil {
		return err
	}
	receiptDirectory, receiptPath, err := newDocumentationIntegrationReceiptOutput(root)
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(receiptDirectory); cleanupErr != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("remove documentation integration receipt: %w", cleanupErr),
			)
		}
	}()

	plan := documentationIntegrationCheckPlan(root, receiptPath)
	if err := runCheckSteps(ctx, commands, plan.beforeReceipt); err != nil {
		return err
	}
	summary, err := readAndValidateGoldenPathAttestation(
		ctx, commands, root, receiptPath,
	)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(
		os.Stdout,
		"Golden-path receipt verified: source=%s sample-lock=%s runtime=node-%s/chromium-%s\n",
		abbreviateDigest(summary.SourceRevision),
		abbreviateDigest(summary.PackageLockSHA256),
		goldenPathRequiredNodeVersion,
		goldenPathRequiredBrowserVersion,
	)
	return runCheckSteps(ctx, commands, plan.afterReceipt)
}

func runCheckSteps(
	ctx context.Context,
	commands commandExecutor,
	steps []checkStep,
) error {
	for _, step := range steps {
		if err := runCommandWithEnvironment(
			ctx,
			commands,
			step.directory,
			step.environment,
			step.name,
			step.arguments...,
		); err != nil {
			return err
		}
	}
	return nil
}

func checkGoFormat(
	ctx context.Context,
	root string,
	commands commandExecutor,
) error {
	output, err := commands.Output(ctx, checkStep{
		directory: root,
		name:      "git",
		arguments: []string{"ls-files", "-z", "--", "*.go"},
	})
	if err != nil {
		return errors.New("list tracked Go files")
	}
	raw := strings.Split(string(output), "\x00")
	files := make([]string, 0, len(raw))
	for _, file := range raw {
		if file != "" {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return errors.New("tracked Go file inventory is empty")
	}
	arguments := append([]string{"-l"}, files...)
	unformatted, err := commands.Output(ctx, checkStep{
		directory: root,
		name:      "gofmt",
		arguments: arguments,
	})
	if err != nil {
		return errors.New("run gofmt inventory")
	}
	if len(unformatted) != 0 {
		_, _ = os.Stdout.Write(unformatted)
		return errors.New("tracked Go files require gofmt")
	}
	return nil
}

func checkWeb(
	ctx context.Context,
	root string,
	commands commandExecutor,
) error {
	webRoot := filepath.Join(root, "web")
	if err := requireVersion(
		ctx, commands, webRoot, "bun", "1.3.11", "--version",
	); err != nil {
		return err
	}
	steps := [][]string{
		{"bun", "install", "--frozen-lockfile"},
		{"bun", "run", "lint"},
		{"bun", "run", "test"},
		{"bunx", "tsc", "-b"},
		{"bun", "run", "build"},
	}
	for _, step := range steps {
		if err := runCommand(
			ctx, commands, webRoot, step[0], step[1:]...,
		); err != nil {
			return err
		}
	}
	return requireCleanBundle(
		ctx,
		commands,
		root,
		"internal/access/manager/webui/dist",
	)
}

func checkDemo(
	ctx context.Context,
	root string,
	commands commandExecutor,
) error {
	demoRoot := filepath.Join(root, "demo", "chatdemo")
	if err := requireVersion(
		ctx, commands, demoRoot, "node", "v22.12.0", "--version",
	); err != nil {
		return err
	}
	if err := requireVersion(
		ctx, commands, demoRoot, "yarn", "1.22.22", "--version",
	); err != nil {
		return err
	}
	steps := [][]string{
		{"yarn", "install", "--frozen-lockfile"},
		{"yarn", "test"},
		{"yarn", "build"},
	}
	for _, step := range steps {
		if err := runCommand(
			ctx, commands, demoRoot, step[0], step[1:]...,
		); err != nil {
			return err
		}
	}
	return requireCleanBundle(
		ctx,
		commands,
		root,
		"internal/access/api/demoui/dist",
	)
}

func checkThreeNode(
	ctx context.Context,
	root string,
	commands commandExecutor,
) error {
	if err := commands.Run(ctx, checkStep{
		directory: root,
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
	}); err != nil {
		return errors.New("three-node cluster smoke failed")
	}
	return nil
}

func requireVersion(
	ctx context.Context,
	commands commandExecutor,
	directory string,
	name string,
	expected string,
	arguments ...string,
) error {
	output, err := commands.Output(ctx, checkStep{
		directory: directory,
		name:      name,
		arguments: arguments,
	})
	if err != nil {
		return fmt.Errorf("run CI check tool %s version: %w", name, err)
	}
	if strings.TrimSpace(string(output)) != expected {
		return fmt.Errorf("unexpected CI check tool %s version", name)
	}
	return nil
}

func requireCleanBundle(
	ctx context.Context,
	commands commandExecutor,
	root string,
	bundlePath string,
) error {
	output, err := commands.Output(ctx, checkStep{
		directory: root,
		name:      "git",
		arguments: []string{
			"-c", "core.hooksPath=/dev/null",
			"-c", "core.fsmonitor=false",
			"-c", "diff.external=",
			"status", "--porcelain", "--", bundlePath,
		},
	})
	if err != nil {
		return errors.New("inspect generated bundle")
	}
	if len(output) != 0 {
		_, _ = os.Stdout.Write(output)
		return errors.New("generated embedded bundle is stale")
	}
	return nil
}

func runCommand(
	ctx context.Context,
	commands commandExecutor,
	directory string,
	name string,
	arguments ...string,
) error {
	return runCommandWithEnvironment(
		ctx, commands, directory, nil, name, arguments...,
	)
}

func runCommandWithEnvironment(
	ctx context.Context,
	commands commandExecutor,
	directory string,
	environment []string,
	name string,
	arguments ...string,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("CI check command %s canceled: %w", name, err)
	}
	if err := commands.Run(ctx, checkStep{
		directory:   directory,
		name:        name,
		arguments:   arguments,
		environment: environment,
	}); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf(
				"CI check command %s canceled: %w", name, contextErr,
			)
		}
		return fmt.Errorf("CI check command %s failed", name)
	}
	return nil
}

func mergeEnvironment(base, overrides []string) []string {
	replacedKeys := make(map[string]struct{}, len(overrides))
	lastOverride := make(map[string]int, len(overrides))
	for index, entry := range overrides {
		key := environmentKey(entry)
		replacedKeys[key] = struct{}{}
		lastOverride[key] = index
	}
	merged := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		if _, replace := replacedKeys[environmentKey(entry)]; !replace {
			merged = append(merged, entry)
		}
	}
	for index, entry := range overrides {
		if lastOverride[environmentKey(entry)] == index {
			merged = append(merged, entry)
		}
	}
	return merged
}

func environmentKey(entry string) string {
	key, _, _ := strings.Cut(entry, "=")
	return key
}
