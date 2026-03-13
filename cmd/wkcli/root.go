package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Config represents the CLI configuration file structure.
type Config struct {
	Current  string                `yaml:"current"`
	Contexts map[string]*Context   `yaml:"contexts"`
}

// Context represents a single server connection context.
type Context struct {
	Server string `yaml:"server"`
	Token  string `yaml:"token,omitempty"`
}

var (
	cfg        Config
	cfgPath    string
	flagServer string
	flagToken  string
)

var rootCmd = &cobra.Command{
	Use:   "wkcli",
	Short: "WuKongIM CLI - command line tool for WuKongIM",
	Long: `wkcli is a command line tool for interacting with WuKongIM server.
It supports HTTP API calls and WebSocket long-connection chat.

Quick start:
  wkcli context set --server http://localhost:5001
  wkcli health
  wkcli chat --uid user1 --token abc123 --channel group1 --channel_type 2`,
}

func init() {
	cobra.OnInitialize(loadConfig)
	rootCmd.PersistentFlags().StringVar(&flagServer, "server", "", "server address (overrides context)")
	rootCmd.PersistentFlags().StringVar(&flagToken, "token", "", "auth token (overrides context)")
}

func configPath() string {
	if cfgPath != "" {
		return cfgPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".wkcli.yaml"
	}
	cfgPath = filepath.Join(home, ".wkcli.yaml")
	return cfgPath
}

func loadConfig() {
	data, err := os.ReadFile(configPath())
	if err != nil {
		// No config file yet, use defaults.
		cfg = Config{
			Current:  "default",
			Contexts: map[string]*Context{},
		}
		return
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		cfg = Config{
			Current:  "default",
			Contexts: map[string]*Context{},
		}
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]*Context{}
	}
}

func saveConfig() error {
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0o644)
}

func getServerURL() string {
	if flagServer != "" {
		return flagServer
	}
	if ctx, ok := cfg.Contexts[cfg.Current]; ok {
		return ctx.Server
	}
	return ""
}

func getToken() string {
	if flagToken != "" {
		return flagToken
	}
	if ctx, ok := cfg.Contexts[cfg.Current]; ok {
		return ctx.Token
	}
	return ""
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
