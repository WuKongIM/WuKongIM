package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadCommandConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, []byte(`{"bootstrap":{},"provider":{},"access_key":"forbidden"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCommandConfig(path); err == nil {
		t.Fatal("readCommandConfig() error = nil, want unknown-field rejection")
	}
}

func TestReadCommandConfigRejectsTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, []byte(`{"bootstrap":{},"provider":{}} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCommandConfig(path); err == nil {
		t.Fatal("readCommandConfig() error = nil, want trailing-data rejection")
	}
}
