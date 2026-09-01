package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/app"
	productconfig "github.com/WuKongIM/WuKongIM/internal/config"
	"github.com/mattn/go-isatty"
)

const (
	exitUsage  = 64
	exitConfig = 78
)

type commandIO struct {
	stdin          io.Reader
	stdout         io.Writer
	stderr         io.Writer
	stdoutTerminal bool
}

type commandError struct {
	code int
	err  error
}

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }

func newCommandError(code int, err error) error {
	if err == nil {
		return nil
	}
	return &commandError{code: code, err: err}
}

func commandExitCode(err error) int {
	var commandErr *commandError
	if errors.As(err, &commandErr) {
		return commandErr.code
	}
	if errors.Is(err, app.ErrInvalidConfig) {
		return exitConfig
	}
	return 1
}

func execute(ctx context.Context, args []string, streams commandIO, newApp appFactory) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return run(ctx, args, newApp)
	}
	switch args[0] {
	case "version":
		return runVersionCommand(args[1:], streams.stdout)
	case "config":
		return runConfigCommand(args[1:], streams)
	default:
		return newCommandError(exitUsage, fmt.Errorf("unknown command %q; expected version, config, or -config", args[0]))
	}
}

func runVersionCommand(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("wukongim version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	output := fs.String("output", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return newCommandError(exitUsage, fmt.Errorf("version: %w", err))
	}
	if fs.NArg() != 0 {
		return newCommandError(exitUsage, fmt.Errorf("version: unexpected arguments: %s", strings.Join(fs.Args(), " ")))
	}
	info := currentBuildInfo()
	switch strings.ToLower(strings.TrimSpace(*output)) {
	case "text":
		_, err := fmt.Fprintf(stdout, "wukongim version=%s commit=%s source=%s\n", info.Version, info.Commit, info.BuildSource)
		return err
	case "json":
		return json.NewEncoder(stdout).Encode(info)
	default:
		return newCommandError(exitUsage, fmt.Errorf("version: --output must be text or json"))
	}
}

func runConfigCommand(args []string, streams commandIO) error {
	if len(args) == 0 {
		return newCommandError(exitUsage, fmt.Errorf("config: expected init or validate"))
	}
	switch args[0] {
	case "init":
		return runConfigInitCommand(args[1:], streams)
	case "validate":
		return runConfigValidateCommand(args[1:], streams.stdout)
	default:
		return newCommandError(exitUsage, fmt.Errorf("config: unknown command %q; expected init or validate", args[0]))
	}
}

func runConfigInitCommand(args []string, streams commandIO) error {
	fs := flag.NewFlagSet("wukongim config init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to the new wukongim.toml")
	gatewayPublic := fs.Bool("gateway-public", false, "listen for TCP and WebSocket clients on all interfaces")
	passwordStdin := fs.Bool("admin-password-stdin", false, "read the initial manager password from stdin")
	if err := fs.Parse(args); err != nil {
		return newCommandError(exitUsage, fmt.Errorf("config init: %w", err))
	}
	if fs.NArg() != 0 {
		return newCommandError(exitUsage, fmt.Errorf("config init: unexpected arguments: %s", strings.Join(fs.Args(), " ")))
	}
	if strings.TrimSpace(*configPath) == "" {
		return newCommandError(exitUsage, fmt.Errorf("config init: --config is required"))
	}
	adminPassword := ""
	generatedPassword := false
	if *passwordStdin {
		body, err := io.ReadAll(io.LimitReader(streams.stdin, 4097))
		if err != nil {
			return newCommandError(exitUsage, fmt.Errorf("config init: read manager password: %w", err))
		}
		if len(body) > 4096 {
			return newCommandError(exitUsage, fmt.Errorf("config init: manager password exceeds 4096 bytes"))
		}
		adminPassword = strings.TrimRight(string(body), "\r\n")
		if strings.TrimSpace(adminPassword) == "" {
			return newCommandError(exitUsage, fmt.Errorf("config init: manager password from stdin is empty"))
		}
	} else {
		if !streams.stdoutTerminal {
			return newCommandError(exitUsage, fmt.Errorf("config init: non-interactive use requires --admin-password-stdin"))
		}
		generatedPassword = true
	}
	result, err := productconfig.Init(productconfig.InitOptions{
		Path:          *configPath,
		GatewayPublic: *gatewayPublic,
		AdminPassword: adminPassword,
	})
	if err != nil {
		return newCommandError(exitConfig, err)
	}
	if _, err := fmt.Fprintf(streams.stdout, "configuration created: %s\ncluster id: %s\nmanager username: %s\n",
		result.Path, result.ClusterID, result.AdminUsername); err != nil {
		return err
	}
	if generatedPassword {
		if _, err := fmt.Fprintf(streams.stdout, "manager password: %s\n", result.AdminPassword); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(streams.stdout, "validate with: wukongim config validate --config %s\n", result.Path)
	return err
}

func runConfigValidateCommand(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("wukongim config validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to wukongim.toml")
	if err := fs.Parse(args); err != nil {
		return newCommandError(exitUsage, fmt.Errorf("config validate: %w", err))
	}
	if fs.NArg() != 0 {
		return newCommandError(exitUsage, fmt.Errorf("config validate: unexpected arguments: %s", strings.Join(fs.Args(), " ")))
	}
	if strings.TrimSpace(*configPath) == "" {
		return newCommandError(exitUsage, fmt.Errorf("config validate: --config is required"))
	}
	cfg, err := loadConfig([]string{"-config", *configPath})
	if err != nil {
		return newCommandError(exitConfig, err)
	}
	if _, err := app.NormalizeConfig(cfg); err != nil {
		return newCommandError(exitConfig, err)
	}
	_, err = fmt.Fprintf(stdout, "configuration valid: %s\n", cfg.ConfigPath)
	return err
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
