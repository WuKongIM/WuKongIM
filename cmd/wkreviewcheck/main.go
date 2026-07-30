// Command wkreviewcheck runs fixed composite checks from the trusted Review
// Agent control tree. It accepts a selector, never an arbitrary command.
package main

import (
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
		return errors.New("review check selector is required")
	}
	root, err := os.Getwd()
	if err != nil {
		return errors.New("resolve Review check workspace")
	}
	switch args[0] {
	case "go-format":
		return checkGoFormat(root)
	case "go-mod-tidy":
		return runCommand(root, "go", "mod", "tidy", "-diff")
	case "web":
		return checkWeb(root)
	case "demo":
		return checkDemo(root)
	case "three-node":
		return checkThreeNode(root)
	default:
		return errors.New("unknown Review check selector")
	}
}

func checkGoFormat(root string) error {
	command := exec.Command("git", "ls-files", "-z", "--", "*.go")
	command.Dir = root
	output, err := command.Output()
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
	command = exec.Command("gofmt", arguments...)
	command.Dir = root
	command.Stderr = os.Stderr
	unformatted, err := command.Output()
	if err != nil {
		return errors.New("run gofmt inventory")
	}
	if len(unformatted) != 0 {
		_, _ = os.Stdout.Write(unformatted)
		return errors.New("tracked Go files require gofmt")
	}
	return nil
}

func checkWeb(root string) error {
	webRoot := filepath.Join(root, "web")
	if err := requireVersion(webRoot, "bun", "1.3.11", "--version"); err != nil {
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
		if err := runCommand(webRoot, step[0], step[1:]...); err != nil {
			return err
		}
	}
	return requireCleanBundle(
		root,
		"internal/access/manager/webui/dist",
	)
}

func checkDemo(root string) error {
	demoRoot := filepath.Join(root, "demo", "chatdemo")
	if err := requireVersion(demoRoot, "node", "v22.12.0", "--version"); err != nil {
		return err
	}
	if err := requireVersion(demoRoot, "yarn", "1.22.22", "--version"); err != nil {
		return err
	}
	steps := [][]string{
		{"yarn", "install", "--frozen-lockfile"},
		{"yarn", "test"},
		{"yarn", "build"},
	}
	for _, step := range steps {
		if err := runCommand(demoRoot, step[0], step[1:]...); err != nil {
			return err
		}
	}
	return requireCleanBundle(
		root,
		"internal/access/api/demoui/dist",
	)
}

func checkThreeNode(root string) error {
	command := exec.Command(
		"bash",
		"scripts/smoke-wkcli-sim-wukongim-three-nodes.sh",
		"--out-dir", ".review-agent-output/three-node-smoke",
		"--ready-timeout", "180",
	)
	command.Dir = root
	command.Stdin = nil
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = append(
		os.Environ(),
		"WK_WUKONGIM_THREE_NODES_PROMETHEUS_ENABLE=false",
		"WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE=false",
		"WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_CONTROLLER_VOTER=false",
		"WK_WKCLI_SIM_THREE_SMOKE_FAULT_KILL_NODE=false",
	)
	if err := command.Run(); err != nil {
		return errors.New("three-node cluster smoke failed")
	}
	return nil
}

func requireVersion(
	directory string,
	name string,
	expected string,
	arguments ...string,
) error {
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Stdin = nil
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("run Review check tool %s version: %w", name, err)
	}
	if strings.TrimSpace(string(output)) != expected {
		return fmt.Errorf("unexpected Review check tool %s version", name)
	}
	return nil
}

func requireCleanBundle(root string, bundlePath string) error {
	command := exec.Command(
		"git",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
		"status", "--porcelain", "--", bundlePath,
	)
	command.Dir = root
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return errors.New("inspect generated bundle")
	}
	if len(output) != 0 {
		_, _ = os.Stdout.Write(output)
		return errors.New("generated embedded bundle is stale")
	}
	return nil
}

func runCommand(directory string, name string, arguments ...string) error {
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Stdin = nil
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("Review check command %s failed", name)
	}
	return nil
}
