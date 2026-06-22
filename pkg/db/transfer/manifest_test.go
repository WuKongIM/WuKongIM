package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestAcceptsValidBundle(t *testing.T) {
	root := t.TempDir()
	writeBundleFile(t, root, "meta/users.jsonl", []byte("{\"hash_slot\":1,\"uid\":\"u1\"}\n"))
	sum := sha256.Sum256([]byte("{\"hash_slot\":1,\"uid\":\"u1\"}\n"))
	writeManifest(t, root, `{
        "format":"wkdb-import-bundle",
        "version":1,
        "hash_slot_count":16,
        "files":[{"path":"meta/users.jsonl","kind":"meta.users","rows":1,"sha256":"`+hex.EncodeToString(sum[:])+`"}]
    }`)

	manifest, err := LoadManifest(root)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	if manifest.HashSlotCount != 16 || len(manifest.Files) != 1 {
		t.Fatalf("manifest = %+v, want hash-slot count 16 and one file", manifest)
	}
}

func TestLoadManifestRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, `{
        "format":"wkdb-import-bundle",
        "version":1,
        "hash_slot_count":16,
        "files":[{"path":"../escape.jsonl","kind":"meta.users","rows":0,"sha256":"`+strings.Repeat("0", 64)+`"}]
    }`)

	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("LoadManifest() error = %v, want unsafe path", err)
	}
}

func TestLoadManifestRejectsChecksumMismatch(t *testing.T) {
	root := t.TempDir()
	writeBundleFile(t, root, "meta/users.jsonl", []byte("{}\n"))
	writeManifest(t, root, `{
        "format":"wkdb-import-bundle",
        "version":1,
        "hash_slot_count":16,
        "files":[{"path":"meta/users.jsonl","kind":"meta.users","rows":1,"sha256":"`+strings.Repeat("0", 64)+`"}]
    }`)

	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("LoadManifest() error = %v, want checksum error", err)
	}
}

func TestLoadManifestRejectsUnknownKind(t *testing.T) {
	root := t.TempDir()
	writeBundleFile(t, root, "meta/unknown.jsonl", []byte(""))
	sum := sha256.Sum256(nil)
	writeManifest(t, root, `{
        "format":"wkdb-import-bundle",
        "version":1,
        "hash_slot_count":16,
        "files":[{"path":"meta/unknown.jsonl","kind":"meta.unknown","rows":0,"sha256":"`+hex.EncodeToString(sum[:])+`"}]
    }`)

	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("LoadManifest() error = %v, want unknown kind", err)
	}
}

func TestLoadManifestRejectsSymlinkFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	data := []byte("{}\n")
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", outside, err)
	}

	link := filepath.Join(root, "meta/users.jsonl")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(link), err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("Symlink(%s, %s): %v", outside, link, err)
	}
	sum := sha256.Sum256(data)
	writeManifest(t, root, `{
        "format":"wkdb-import-bundle",
        "version":1,
        "hash_slot_count":16,
        "files":[{"path":"meta/users.jsonl","kind":"meta.users","rows":1,"sha256":"`+hex.EncodeToString(sum[:])+`"}]
    }`)

	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("LoadManifest() error = %v, want symlink error", err)
	}
}

func TestLoadManifestRejectsNonRegularFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "meta/users.jsonl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	sum := sha256.Sum256(nil)
	writeManifest(t, root, `{
        "format":"wkdb-import-bundle",
        "version":1,
        "hash_slot_count":16,
        "files":[{"path":"meta/users.jsonl","kind":"meta.users","rows":0,"sha256":"`+hex.EncodeToString(sum[:])+`"}]
    }`)

	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("LoadManifest() error = %v, want non-regular error", err)
	}
}

func TestLoadManifestRejectsUppercaseSHA256(t *testing.T) {
	root := t.TempDir()
	data := []byte("{}\n")
	writeBundleFile(t, root, "meta/users.jsonl", data)
	sum := sha256.Sum256(data)
	writeManifest(t, root, `{
        "format":"wkdb-import-bundle",
        "version":1,
        "hash_slot_count":16,
        "files":[{"path":"meta/users.jsonl","kind":"meta.users","rows":1,"sha256":"`+strings.ToUpper(hex.EncodeToString(sum[:]))+`"}]
    }`)

	_, err := LoadManifest(root)
	if err == nil || !strings.Contains(err.Error(), "lowercase") {
		t.Fatalf("LoadManifest() error = %v, want lowercase sha256 error", err)
	}
}

func writeBundleFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func writeManifest(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest.json): %v", err)
	}
}
