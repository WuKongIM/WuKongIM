package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

func TestRunRejectsIncompleteGlobalArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no command", want: "usage: wkdb"},
		{name: "flags without command", args: []string{"--format", "json"}, want: "usage: wkdb"},
		{name: "query without SQL", args: []string{"query"}, want: "query <sql>"},
		{name: "unknown global flag", args: []string{"--unknown"}, want: "flag provided but not defined"},
		{name: "hash slot overflow", args: []string{"--hash-slot-count", "65536", "query", "show tables"}, want: "must be <= 65535"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runWithStreams(test.args, nil, &stdout, &stderr); code != exitConfig {
				t.Fatalf("runWithStreams() code = %d, want %d", code, exitConfig)
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestCommandLocalFlagValidation(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		parse func([]string, *bytes.Buffer) int
		want  string
	}{
		{
			name: "import unknown flag",
			args: []string{"--unknown"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseImportCommandFlags(args, stderr)
				return code
			},
			want: "flag provided but not defined",
		},
		{
			name: "import positional argument",
			args: []string{"bundle"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseImportCommandFlags(args, stderr)
				return code
			},
			want: "unexpected argument",
		},
		{
			name: "import negative batch",
			args: []string{"--subscriber-batch-size", "-1"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseImportCommandFlags(args, stderr)
				return code
			},
			want: "batch sizes must be non-negative",
		},
		{
			name: "export unknown flag",
			args: []string{"--unknown"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseExportCommandFlags(args, stderr)
				return code
			},
			want: "flag provided but not defined",
		},
		{
			name: "export positional argument",
			args: []string{"bundle"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseExportCommandFlags(args, stderr)
				return code
			},
			want: "unexpected argument",
		},
		{
			name: "export negative page size",
			args: []string{"--page-size", "-1"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseExportCommandFlags(args, stderr)
				return code
			},
			want: "page sizes must be non-negative",
		},
		{
			name: "diff unknown flag",
			args: []string{"--unknown"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseDiffCommandFlags(args, stderr)
				return code
			},
			want: "flag provided but not defined",
		},
		{
			name: "diff positional argument",
			args: []string{"source"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseDiffCommandFlags(args, stderr)
				return code
			},
			want: "unexpected argument",
		},
		{
			name: "diff negative page size",
			args: []string{"--page-size", "-1"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseDiffCommandFlags(args, stderr)
				return code
			},
			want: "page size must be non-negative",
		},
		{
			name: "diff unknown mode",
			args: []string{"--mode", "fast"},
			parse: func(args []string, stderr *bytes.Buffer) int {
				_, code := parseDiffCommandFlags(args, stderr)
				return code
			},
			want: "unknown mode",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := test.parse(test.args, &stderr); code != exitConfig {
				t.Fatalf("parse() code = %d, want %d", code, exitConfig)
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
		})
	}
}

func TestRunDiffRejectsInvalidConfigurationBeforeOpeningStores(t *testing.T) {
	t.Setenv("WK_CLUSTER_HASH_SLOT_COUNT", "")
	t.Setenv("WK_CLUSTER_INITIAL_SLOT_COUNT", "")
	t.Setenv("WK_CLUSTER_SLOT_COUNT", "")

	tests := []struct {
		name   string
		global cliFlags
		args   []string
		want   string
	}{
		{
			name:   "unknown format",
			global: cliFlags{format: "yaml", hashSlotCount: 16},
			args:   []string{"--source-data-dir", "source", "--target-data-dir", "target"},
			want:   "unknown format",
		},
		{
			name: "missing hash slot count",
			args: []string{"--source-data-dir", "source", "--target-data-dir", "target"},
			want: "requires --hash-slot-count",
		},
		{
			name:   "missing source",
			global: cliFlags{hashSlotCount: 16},
			args:   []string{"--target-data-dir", "target"},
			want:   "source requires metadata and message storage paths",
		},
		{
			name:   "missing target",
			global: cliFlags{hashSlotCount: 16},
			args:   []string{"--source-data-dir", "source"},
			want:   "target requires metadata and message storage paths",
		},
		{
			name:   "unreadable config",
			global: cliFlags{configPath: filepath.Join(t.TempDir(), "missing.toml")},
			args:   []string{"--source-data-dir", "source", "--target-data-dir", "target"},
			want:   "read config",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runDiff(context.Background(), test.global, test.args, &stdout, &stderr); code != exitConfig {
				t.Fatalf("runDiff() code = %d, want %d", code, exitConfig)
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunExportRejectsInvalidConfigurationBeforeOpeningStore(t *testing.T) {
	t.Setenv("WK_CLUSTER_HASH_SLOT_COUNT", "")
	t.Setenv("WK_CLUSTER_INITIAL_SLOT_COUNT", "")
	t.Setenv("WK_CLUSTER_SLOT_COUNT", "")

	tests := []struct {
		name   string
		global cliFlags
		args   []string
		want   string
	}{
		{name: "missing output", want: "--output is required"},
		{
			name:   "missing hash slot count",
			global: cliFlags{dataDir: t.TempDir()},
			args:   []string{"--output", filepath.Join(t.TempDir(), "bundle")},
			want:   "requires --hash-slot-count",
		},
		{
			name:   "partial storage paths",
			global: cliFlags{metaPath: filepath.Join(t.TempDir(), "meta"), hashSlotCount: 16},
			args:   []string{"--output", filepath.Join(t.TempDir(), "bundle")},
			want:   "requires both metadata and message storage paths",
		},
		{
			name:   "unreadable config",
			global: cliFlags{configPath: filepath.Join(t.TempDir(), "missing.toml")},
			args:   []string{"--output", filepath.Join(t.TempDir(), "bundle")},
			want:   "read config",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runExport(context.Background(), test.global, test.args, &stdout, &stderr); code != exitConfig {
				t.Fatalf("runExport() code = %d, want %d", code, exitConfig)
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunImportDryRunRejectsTrailingManifestJSON(t *testing.T) {
	bundleRoot := t.TempDir()
	manifest := `{"format":"wkdb-import-bundle","version":1,"hash_slot_count":16,"files":[]}` + "\n" + `{"unexpected":true}` + "\n"
	if err := os.WriteFile(filepath.Join(bundleRoot, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest): %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runImport(context.Background(), cliFlags{}, []string{"--dry-run", "--input", bundleRoot}, &stdout, &stderr)
	if code != exitQuery {
		t.Fatalf("runImport() code = %d, want %d", code, exitQuery)
	}
	if !strings.Contains(stderr.String(), "extra JSON data") {
		t.Fatalf("stderr = %q, want strict trailing-data error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunImportRejectsPartialWritableTargetBeforeOpeningStore(t *testing.T) {
	bundleRoot := t.TempDir()
	writeWKDBBundle(t, bundleRoot, 16, nil)

	var stdout, stderr bytes.Buffer
	code := runImport(context.Background(), cliFlags{
		metaPath:      filepath.Join(t.TempDir(), "meta"),
		hashSlotCount: 16,
	}, []string{"--input", bundleRoot, "--require-empty"}, &stdout, &stderr)
	if code != exitConfig {
		t.Fatalf("runImport() code = %d, want %d", code, exitConfig)
	}
	if !strings.Contains(stderr.String(), "requires both metadata and message storage paths") {
		t.Fatalf("stderr = %q, want complete storage-path error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestCLIConfigRejectsUnreadableMalformedAndNonScalarTOML(t *testing.T) {
	tests := []struct {
		name string
		body *string
		want string
	}{
		{name: "unreadable", want: "read config"},
		{name: "malformed", body: stringPointer("[cluster"), want: "parse config"},
		{name: "non scalar", body: stringPointer("[cluster]\nhash_slot_count = true\n"), want: "value must be a string or integer"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "wukongim.toml")
			if test.body != nil {
				if err := os.WriteFile(configPath, []byte(*test.body), 0o600); err != nil {
					t.Fatalf("WriteFile(config): %v", err)
				}
			}
			_, err := resolveCLIConfig(cliFlags{configPath: configPath, dataDir: t.TempDir()}, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("resolveCLIConfig() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveCLIConfigUsesConfiguredStoragePaths(t *testing.T) {
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "configured-meta")
	messagePath := filepath.Join(dir, "configured-messages")
	configPath := filepath.Join(dir, "wukongim.toml")
	body := fmt.Sprintf("[storage]\ndb_path = %q\nchannel_log_path = %q\n", metaPath, messagePath)
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}

	cfg, err := resolveCLIConfig(cliFlags{configPath: configPath}, nil)
	if err != nil {
		t.Fatalf("resolveCLIConfig(): %v", err)
	}
	if cfg.options.MetaPath != metaPath || cfg.options.MessagePath != messagePath {
		t.Fatalf("resolved paths = %q/%q, want %q/%q", cfg.options.MetaPath, cfg.options.MessagePath, metaPath, messagePath)
	}
}

func TestTransferErrorsMapToStableExitCodes(t *testing.T) {
	mappers := []struct {
		name string
		mapf func(error) int
	}{
		{name: "import", mapf: importTransferExitCode},
		{name: "export", mapf: exportTransferExitCode},
		{name: "diff", mapf: diffTransferExitCode},
	}
	errorsToMap := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid bundle", err: fmt.Errorf("load: %w", transfer.ErrInvalidBundle), want: exitQuery},
		{name: "validation", err: fmt.Errorf("validate: %w", transfer.ErrValidation), want: exitQuery},
		{name: "internal", err: errors.New("storage unavailable"), want: exitInternal},
	}

	for _, mapper := range mappers {
		for _, test := range errorsToMap {
			t.Run(mapper.name+"/"+test.name, func(t *testing.T) {
				if got := mapper.mapf(test.err); got != test.want {
					t.Fatalf("exit code = %d, want %d", got, test.want)
				}
			})
		}
	}
}

func stringPointer(value string) *string {
	return &value
}
