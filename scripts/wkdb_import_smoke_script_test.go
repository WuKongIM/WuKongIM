package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWKDBImportSmokeScriptValidatesAndImportsBundle(t *testing.T) {
	root := repoRoot(t)
	wkdbBin := filepath.Join(t.TempDir(), "wkdb")
	build := exec.Command(goTool(t), "build", "-o", wkdbBin, "./cmd/wkdb")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wkdb failed: %v\n%s", err, output)
	}

	workDir := t.TempDir()
	cmd := exec.Command("bash", "scripts/wkdb-import-smoke.sh", "--work-dir", workDir)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "WKDB_BIN="+wkdbBin)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke script failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"dry-run ok",
		"import ok",
		"query ok",
		"wkdb import smoke passed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("script output missing %q:\n%s", want, text)
		}
	}
	for _, noisy := range []string{"Found 1 WALs", "[JOB 1] WAL"} {
		if strings.Contains(text, noisy) {
			t.Fatalf("script output should not include normal Pebble replay logs %q:\n%s", noisy, text)
		}
	}
}

func TestWKDBExportRoundTripSmokeScript(t *testing.T) {
	root := repoRoot(t)
	wkdbBin := filepath.Join(t.TempDir(), "wkdb")
	build := exec.Command(goTool(t), "build", "-o", wkdbBin, "./cmd/wkdb")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wkdb failed: %v\n%s", err, output)
	}

	workDir := t.TempDir()
	cmd := exec.Command("bash", "scripts/wkdb-export-roundtrip-smoke.sh", "--work-dir", workDir)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "WKDB_BIN="+wkdbBin)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("export smoke script failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"export ok",
		"roundtrip import ok",
		"wkdb export roundtrip smoke passed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("script output missing %q:\n%s", want, text)
		}
	}
}

func TestWKDBDiffSmokeScript(t *testing.T) {
	root := repoRoot(t)
	wkdbBin := filepath.Join(t.TempDir(), "wkdb")
	build := exec.Command(goTool(t), "build", "-o", wkdbBin, "./cmd/wkdb")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build wkdb failed: %v\n%s", err, output)
	}

	workDir := t.TempDir()
	cmd := exec.Command("bash", "scripts/wkdb-diff-smoke.sh", "--work-dir", workDir)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "WKDB_BIN="+wkdbBin)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("diff smoke script failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{"diff equal ok", "diff mismatch ok", "wkdb diff smoke passed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("script output missing %q:\n%s", want, text)
		}
	}
}

func goTool(t *testing.T) string {
	t.Helper()
	if goroot := runtime.GOROOT(); goroot != "" {
		return filepath.Join(goroot, "bin", "go")
	}
	return "go"
}
