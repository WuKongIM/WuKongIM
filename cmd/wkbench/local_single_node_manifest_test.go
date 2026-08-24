package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseLocalSingleNodeManifestStreamsPayloadLargerThanFormerMemoryCap(t *testing.T) {
	const simulatedEntryBytes = 100 << 20
	var manifest strings.Builder
	expected := make(map[string]string)
	for index := 0; index < 4; index++ {
		name := "metrics/step-" + strconv.Itoa(index) + ".prom"
		digest := fmt.Sprintf("%064x", index+1)
		expected[name] = digest
		fmt.Fprintf(&manifest, "%s  %s\n", digest, name)
	}
	var streamedBytes int64
	entries, err := parseLocalSingleNodeChecksumManifest([]byte(manifest.String()), func(relative, digest string) error {
		if expected[relative] != digest {
			t.Fatalf("digest for %s = %s", relative, digest)
		}
		streamedBytes += simulatedEntryBytes
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 || streamedBytes != 400<<20 {
		t.Fatalf("entries/streamed bytes = %d/%d", len(entries), streamedBytes)
	}
}

func TestVerifiedLocalSingleNodeManifestRejectsTypedInputChangedAfterStreamingVerification(t *testing.T) {
	root := t.TempDir()
	payloadPath := filepath.Join(root, "payload.json")
	original := []byte(`{"sealed":true}`)
	if err := os.WriteFile(payloadPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(original)
	manifestPath := filepath.Join(root, "checksums.sha256")
	if err := os.WriteFile(manifestPath, []byte(fmt.Sprintf("%x  payload.json\n", digest)), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := verifyLocalSingleNodeChecksumManifest(root, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payloadPath, []byte(`{"sealed":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.bytesForRelative("payload.json"); err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatalf("changed typed input error = %v", err)
	}
}

func TestVerifyLocalSingleNodeManifestRejectsSymlinkedComponents(t *testing.T) {
	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("sealed")
	if err := os.WriteFile(filepath.Join(realDirectory, "payload.txt"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDirectory, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	manifest := fmt.Sprintf("%x  linked/payload.txt\n", digest)
	manifestPath := filepath.Join(root, "checksums.sha256")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := verifyLocalSingleNodeChecksumManifest(root, manifestPath); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink component error = %v", err)
	}
}

func TestLocalSingleNodeArtifactRootRejectsOutputCollisionAndSymlinkParent(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := openLocalSingleNodeArtifactRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.writeExclusive("reports/result.json", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := root.writeExclusive("reports/result.json", []byte("second")); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("output collision error = %v", err)
	}
	real := filepath.Join(rootPath, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(rootPath, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := root.writeExclusive("linked/result.json", []byte("unsafe")); err == nil {
		t.Fatal("symlink parent was accepted")
	}
}

func TestLocalSingleNodeArtifactRootRejectsRenamedAndReplacedRootDirectory(t *testing.T) {
	parent := t.TempDir()
	original := filepath.Join(parent, "artifacts")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "sealed.txt"), []byte("sealed"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := openLocalSingleNodeArtifactRoot(original)
	if err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(parent, "artifacts-renamed")
	if err := os.Rename(original, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "sealed.txt"), []byte("attacker"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := root.read("sealed.txt", 64); err == nil || !strings.Contains(err.Error(), "root identity changed") {
		t.Fatalf("replaced root error = %v", err)
	}
}
