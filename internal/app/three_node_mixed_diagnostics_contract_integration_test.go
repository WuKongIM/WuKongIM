//go:build integration

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMixedSendDiagnosticFilesAreBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	w := newMixedSendDiagnosticFile(t, path, 8)
	if n, err := w.Write([]byte("12345678")); n != 8 || err != nil {
		t.Fatalf("first write = %d, %v", n, err)
	}
	if n, err := w.Write([]byte("9")); n != 0 || err == nil || !w.overflow {
		t.Fatalf("overflow = %d, %v, %t", n, err, w.overflow)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 8 || info.Mode().Perm() != 0600 {
		t.Fatalf("bounded private file = %v, %v", info, err)
	}
	data, err := readMixedSendDiagnosticFile(path)
	if err != nil || string(data) != "12345678" {
		t.Fatalf("read = %q, %v", data, err)
	}
	if err := os.WriteFile(path, make([]byte, (64<<10)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMixedSendDiagnosticFile(path); err == nil {
		t.Fatal("oversized system snapshot accepted")
	}
	if _, err := readMixedSendDiagnosticFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing signal silently accepted")
	}
}
