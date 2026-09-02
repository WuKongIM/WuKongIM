package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitGeneratesPasswordAndClassifiesEveryEntropyFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.toml")
	result, err := Init(InitOptions{Path: path, RandomReader: deterministicRandomReader()})
	if err != nil {
		t.Fatalf("Init() with generated password: %v", err)
	}
	if len(result.AdminPassword) != 24 || strings.ContainsAny(result.AdminPassword, "+/=") {
		t.Fatalf("generated password = %q, want 18-byte URL-safe base64", result.AdminPassword)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !bytes.Contains(body, []byte(result.AdminPassword)) {
		t.Fatal("generated one-time password is not the credential written to config")
	}

	if _, err := Init(InitOptions{Path: " \t"}); err == nil || !strings.Contains(err.Error(), "--config") {
		t.Fatalf("Init(empty path) error = %v", err)
	}
	want := errors.New("entropy unavailable")
	for failCall, stage := range map[int]string{
		1: "cluster identity", 2: "join token", 3: "manager jwt secret", 4: "manager password",
	} {
		t.Run(stage, func(t *testing.T) {
			calls := 0
			reader := func(data []byte) (int, error) {
				calls++
				if calls == failCall {
					return 0, want
				}
				for i := range data {
					data[i] = byte(calls + i)
				}
				return len(data), nil
			}
			_, err := Init(InitOptions{Path: filepath.Join(t.TempDir(), "config.toml"), RandomReader: reader})
			if !errors.Is(err, want) || !strings.Contains(err.Error(), stage) {
				t.Fatalf("Init() error = %v, want %q wrapping entropy failure", err, stage)
			}
		})
	}
}

func TestRandomHelpersRequireCompleteWellFormedEntropy(t *testing.T) {
	chunks := [][]byte{{1, 2}, {3}, {4, 5}}
	reader := func(data []byte) (int, error) {
		chunk := chunks[0]
		chunks = chunks[1:]
		return copy(data, chunk), nil
	}
	got, err := randomHex(reader, 5)
	if err != nil || got != "0102030405" {
		t.Fatalf("randomHex(partial reads) = (%q, %v)", got, err)
	}
	password, err := randomPassword(func(data []byte) (int, error) {
		for i := range data {
			data[i] = byte(i + 1)
		}
		return len(data), nil
	}, 3)
	if err != nil || password != "AQID" {
		t.Fatalf("randomPassword() = (%q, %v)", password, err)
	}
	for name, reader := range map[string]func([]byte) (int, error){
		"zero":     func([]byte) (int, error) { return 0, nil },
		"negative": func([]byte) (int, error) { return -1, nil },
		"oversized": func(data []byte) (int, error) {
			return len(data) + 1, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := randomHex(reader, 4); err == nil {
				t.Fatal("randomHex() accepted an invalid byte count")
			}
		})
	}
}

func TestWriteValidatedConfigIsAtomicAndLeavesInvalidInputUnpublished(t *testing.T) {
	dir := t.TempDir()
	invalidPath := filepath.Join(dir, "invalid.toml")
	if err := writeValidatedConfig(invalidPath, []byte("not = [valid TOML")); err == nil || !strings.Contains(err.Error(), "validate generated config") {
		t.Fatalf("writeValidatedConfig(invalid) error = %v", err)
	}
	if _, err := os.Lstat(invalidPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid config was published: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".wukongim.toml.*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary config leak = %v, glob error %v", matches, err)
	}

	body, err := renderInitialConfig("cluster", strings.Repeat("a", 64), strings.Repeat("b", 64), "strong-password", false)
	if err != nil {
		t.Fatalf("renderInitialConfig(): %v", err)
	}
	existingPath := filepath.Join(dir, "existing.toml")
	if err := os.WriteFile(existingPath, []byte("operator-owned\n"), 0o600); err != nil {
		t.Fatalf("seed existing config: %v", err)
	}
	if err := writeValidatedConfig(existingPath, body); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("writeValidatedConfig(existing) error = %v", err)
	}
	existing, err := os.ReadFile(existingPath)
	if err != nil || string(existing) != "operator-owned\n" {
		t.Fatalf("existing config changed = %q, error %v", existing, err)
	}

	parentFile := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatalf("seed parent file: %v", err)
	}
	if err := writeValidatedConfig(filepath.Join(parentFile, "config.toml"), body); err == nil || !strings.Contains(err.Error(), "create") {
		t.Fatalf("writeValidatedConfig(parent file) error = %v", err)
	}
}
