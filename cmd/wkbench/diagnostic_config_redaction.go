package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	runtimeconfig "github.com/WuKongIM/WuKongIM/internal/config"
	"github.com/spf13/cobra"
)

func newDiagnosticConfigRedactionCommand() *cobra.Command {
	var inputPath string
	var outputPath string
	cmd := &cobra.Command{
		Use:   "redact-config",
		Short: "Write a schema-validated diagnostic-safe TOML config",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(inputPath) == "" || strings.TrimSpace(outputPath) == "" {
				return commandExit{code: exitConfig, message: "--input and --output are required"}
			}
			if err := writeRedactedDiagnosticConfig(inputPath, outputPath); err != nil {
				return commandExit{code: exitConfig, message: err.Error()}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&inputPath, "input", "", "source wukongim TOML path")
	cmd.Flags().StringVar(&outputPath, "output", "", "new private redacted TOML path")
	return cmd
}

func writeRedactedDiagnosticConfig(inputPath, outputPath string) error {
	inputInfo, err := os.Lstat(inputPath)
	if err != nil {
		return fmt.Errorf("inspect diagnostic config input: %w", err)
	}
	if !inputInfo.Mode().IsRegular() || inputInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("diagnostic config input must be a regular non-symlink file")
	}
	body, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read diagnostic config input: %w", err)
	}
	redacted, err := runtimeconfig.RedactDiagnosticTOML(body)
	if err != nil {
		return err
	}

	outputDir := filepath.Dir(outputPath)
	if info, err := os.Stat(outputDir); err != nil || !info.IsDir() {
		if err != nil {
			return fmt.Errorf("inspect diagnostic config output directory: %w", err)
		}
		return fmt.Errorf("diagnostic config output parent is not a directory")
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return fmt.Errorf("redacted diagnostic config output already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect redacted diagnostic config output: %w", err)
	}
	output, err := os.CreateTemp(outputDir, ".redacted-diagnostic-config-*")
	if err != nil {
		return fmt.Errorf("create private redacted diagnostic config: %w", err)
	}
	temporaryPath := output.Name()
	defer os.Remove(temporaryPath)
	if err := output.Chmod(0o600); err != nil {
		_ = output.Close()
		return fmt.Errorf("protect redacted diagnostic config: %w", err)
	}
	if _, err := output.Write(redacted); err != nil {
		_ = output.Close()
		return fmt.Errorf("write redacted diagnostic config: %w", err)
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return fmt.Errorf("sync redacted diagnostic config: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close redacted diagnostic config: %w", err)
	}
	// Link publishes atomically without replacing a destination created by a
	// concurrent process. Both files are in the same directory/filesystem.
	if err := os.Link(temporaryPath, outputPath); err != nil {
		return fmt.Errorf("publish redacted diagnostic config: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("finalize redacted diagnostic config: %w", err)
	}
	return nil
}
