package clouddeploy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	clouddeployusecase "github.com/WuKongIM/WuKongIM/internal/usecase/clouddeploy"
)

func TestDirectoryRoundTripPreservesContentModeAndDeterministicInventory(t *testing.T) {
	directory, root := openTestDirectory(t)

	if err := directory.WriteFile("config/app.toml", []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldInode := filepath.Join(t.TempDir(), "old-inode")
	if err := os.Link(filepath.Join(root, "config", "app.toml"), oldInode); err != nil {
		t.Fatal(err)
	}
	if err := directory.WriteFile("config/app.toml", []byte("new configuration\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := directory.WriteFile("assets/z.txt", []byte("last\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := directory.WriteFile("assets/a.txt", []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := directory.ReadFile("config/app.toml", int64(len("new configuration\n")))
	if err != nil || string(got) != "new configuration\n" {
		t.Fatalf("ReadFile() = %q, %v", got, err)
	}
	info, err := os.Stat(filepath.Join(root, "config", "app.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o640); got != want {
		t.Fatalf("mode = %04o, want %04o", got, want)
	}
	oldData, err := os.ReadFile(oldInode)
	if err != nil || string(oldData) != "old\n" {
		t.Fatalf("atomic replacement mutated prior inode: %q, %v", oldData, err)
	}
	prefix, err := directory.ReadPrefix("config/app.toml", len("new"))
	if err != nil || string(prefix) != "new" {
		t.Fatalf("ReadPrefix() = %q, %v", prefix, err)
	}
	if _, err := directory.ReadPrefix("assets/a.txt", len("first\n")+1); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadPrefix(short file) error = %v, want io.ErrUnexpectedEOF", err)
	}

	first, err := directory.Files(3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := directory.Files(3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("inventory changed without filesystem mutation:\nfirst=%#v\nsecond=%#v", first, second)
	}
	wantPaths := []string{"assets/a.txt", "assets/z.txt", "config/app.toml"}
	if len(first) != len(wantPaths) {
		t.Fatalf("Files() length = %d, want %d: %#v", len(first), len(wantPaths), first)
	}
	for index, wantPath := range wantPaths {
		if first[index].Path != wantPath {
			t.Fatalf("Files()[%d].Path = %q, want %q", index, first[index].Path, wantPath)
		}
	}
	wantDigest := sha256.Sum256([]byte("first\n"))
	if got, want := first[0].SHA256, hex.EncodeToString(wantDigest[:]); got != want {
		t.Fatalf("Files()[0].SHA256 = %q, want %q", got, want)
	}
	if first[0].Mode != 0o644 || first[0].Size != int64(len("first\n")) {
		t.Fatalf("Files()[0] metadata = %#v", first[0])
	}
	entries, err := os.ReadDir(filepath.Join(root, "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if len(entry.Name()) >= len(".seal-") && entry.Name()[:len(".seal-")] == ".seal-" {
			t.Fatalf("successful atomic write leaked temporary file %q", entry.Name())
		}
	}
}

func TestDirectoryBoundedReadsFailClosed(t *testing.T) {
	directory, _ := openTestDirectory(t)
	if err := directory.WriteFile("payload", []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := directory.ReadFile("payload", 4); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("ReadFile(oversized) error = %v, want ErrInvalidBundle", err)
	}
	if _, err := directory.ReadFile("payload", -1); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("ReadFile(negative limit) error = %v, want ErrInvalidBundle", err)
	}
	got, err := directory.ReadFile("payload", math.MaxInt64)
	if err != nil || string(got) != "12345" {
		t.Fatalf("ReadFile(MaxInt64) = %q, %v", got, err)
	}
	if _, err := directory.ReadPrefix("payload", -1); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("ReadPrefix(negative length) error = %v, want ErrInvalidBundle", err)
	}
	empty, err := directory.ReadPrefix("payload", 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("ReadPrefix(zero) = %q, %v", empty, err)
	}
}

func TestOpenRejectsInvalidRootsAndUnsafeEntries(t *testing.T) {
	base := t.TempDir()
	if _, err := Open(filepath.Join(base, "missing")); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("Open(missing) error = %v, want ErrInvalidBundle", err)
	}
	regular := filepath.Join(base, "regular")
	if err := os.WriteFile(regular, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(regular); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("Open(regular file) error = %v, want ErrInvalidBundle", err)
	}

	realRoot := t.TempDir()
	rootLink := filepath.Join(base, "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(rootLink); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("Open(root symlink) error = %v, want ErrInvalidBundle", err)
	}
	insideLink := filepath.Join(realRoot, "inside-link")
	if err := os.Symlink(regular, insideLink); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(realRoot); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("Open(root with symlink) error = %v, want ErrInvalidBundle", err)
	}
}

func TestDirectoryRejectsTraversalSymlinkAndNonRegularTargets(t *testing.T) {
	directory, root := openTestDirectory(t)
	outsideRoot := t.TempDir()
	outsideFile := filepath.Join(outsideRoot, "outside")
	if err := os.WriteFile(outsideFile, []byte("untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, unsafePath := range []string{"", ".", "..", "../outside", filepath.Join(root, "absolute")} {
		if err := directory.WriteFile(unsafePath, []byte("changed"), 0o600); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
			t.Errorf("WriteFile(%q) error = %v, want ErrInvalidBundle", unsafePath, err)
		}
	}
	if _, err := directory.ReadFile("../outside", 64); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("ReadFile(traversal) error = %v, want ErrInvalidBundle", err)
	}

	if err := os.WriteFile(filepath.Join(root, "blocked"), []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := directory.WriteFile("blocked/child", []byte("data"), 0o600); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("WriteFile(regular parent) error = %v, want ErrInvalidBundle", err)
	}
	if err := os.Mkdir(filepath.Join(root, "target-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := directory.WriteFile("target-dir", []byte("data"), 0o600); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("WriteFile(directory target) error = %v, want ErrInvalidBundle", err)
	}
	if _, err := directory.ReadFile("target-dir", 64); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("ReadFile(directory target) error = %v, want ErrInvalidBundle", err)
	}

	targetLink := filepath.Join(root, "target-link")
	if err := os.Symlink(outsideFile, targetLink); err != nil {
		t.Fatal(err)
	}
	if err := directory.WriteFile("target-link", []byte("changed"), 0o600); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("WriteFile(symlink target) error = %v, want ErrInvalidBundle", err)
	}
	if _, err := directory.ReadFile("target-link", 64); err == nil {
		t.Fatal("ReadFile(symlink target) succeeded")
	}
	if _, err := directory.ReadPrefix("target-link", 1); err == nil {
		t.Fatal("ReadPrefix(symlink target) succeeded")
	}
	parentLink := filepath.Join(root, "parent-link")
	if err := os.Symlink(outsideRoot, parentLink); err != nil {
		t.Fatal(err)
	}
	if err := directory.WriteFile("parent-link/child", []byte("changed"), 0o600); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("WriteFile(symlink parent) error = %v, want ErrInvalidBundle", err)
	}
	if got, err := os.ReadFile(outsideFile); err != nil || string(got) != "untouched\n" {
		t.Fatalf("outside file changed through symlink: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(outsideRoot, "child")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside child exists or stat failed unexpectedly: %v", err)
	}
}

func TestDirectoryFilesEnforcesLimitAndRevalidatesRoot(t *testing.T) {
	directory, root := openTestDirectory(t)
	files, err := directory.Files(0)
	if err != nil || len(files) != 0 {
		t.Fatalf("Files(empty root, zero limit) = %#v, %v", files, err)
	}
	if _, err := directory.Files(-1); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("Files(negative limit) error = %v, want ErrInvalidBundle", err)
	}
	if err := directory.WriteFile("one", []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := directory.Files(0); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("Files(excess entry) error = %v, want ErrInvalidBundle", err)
	}

	unsafeTarget := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(unsafeTarget, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unsafeTarget, filepath.Join(root, "late-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := directory.Files(10); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("Files(root mutated with symlink) error = %v, want ErrInvalidBundle", err)
	}

	if err := os.Remove(filepath.Join(root, "late-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "one")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := directory.Files(10); !errors.Is(err, clouddeployusecase.ErrInvalidBundle) {
		t.Fatalf("Files(replaced root) error = %v, want ErrInvalidBundle", err)
	}
}

func openTestDirectory(t *testing.T) (*Directory, string) {
	t.Helper()
	root := t.TempDir()
	directory, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return directory, root
}
