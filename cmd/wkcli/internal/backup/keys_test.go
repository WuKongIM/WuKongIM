package backup

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	backupkeys "github.com/WuKongIM/WuKongIM/pkg/backup/keypackage"
)

func TestBackupKeysBootstrapAndRecoverWithoutCloudConfiguration(
	t *testing.T,
) {
	root := t.TempDir()
	outputDirectory := filepath.Join(root, "bootstrap")
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{
		"keys", "bootstrap",
		"--repository-id", "repository-production",
		"--out-dir", outputDirectory,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bootstrap Execute(): %v", err)
	}
	packagePath := filepath.Join(
		outputDirectory,
		backupkeys.DeploymentKeyPackageCredentialName,
	)
	kitPath := filepath.Join(
		outputDirectory, "wukongim-backup-recovery.wkr",
	)
	recoveryKeyPath := filepath.Join(
		outputDirectory, "wukongim-backup-recovery.key",
	)
	for _, path := range []string{
		packagePath, kitPath, recoveryKeyPath,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s): %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, info.Mode().Perm())
		}
	}
	if !strings.Contains(stdout.String(), `"repository_id":"repository-production"`) ||
		strings.Contains(stdout.String(), "material") ||
		strings.Contains(stdout.String(), "seed") {
		t.Fatalf("bootstrap stdout leaks or omits metadata: %q", stdout.String())
	}

	original, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	recoveryKeyText, err := os.ReadFile(recoveryKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	recoveryKey, err := base64.StdEncoding.DecodeString(
		strings.TrimSpace(string(recoveryKeyText)),
	)
	if err != nil || len(recoveryKey) != 32 {
		t.Fatalf("recovery key encoding is invalid: len=%d err=%v", len(recoveryKey), err)
	}

	recoveredPath := filepath.Join(root, "recovered-package")
	stdout.Reset()
	stderr.Reset()
	cmd = NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{
		"keys", "recover",
		"--recovery-kit", kitPath,
		"--recovery-key", recoveryKeyPath,
		"--out", recoveredPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("recover Execute(): %v", err)
	}
	recovered, err := os.ReadFile(recoveredPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, recovered) {
		t.Fatal("recovered package differs from bootstrap package")
	}

	cmd = NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{
		"keys", "recover",
		"--recovery-kit", kitPath,
		"--recovery-key", recoveryKeyPath,
		"--out", recoveredPath,
	})
	if err := cmd.Execute(); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("recover overwrite error = %v", err)
	}
}

func TestBackupKeysRotationIsExplicitlyStagedThenActivated(t *testing.T) {
	root := t.TempDir()
	bootstrapDirectory := filepath.Join(root, "bootstrap")
	runBackupKeysCommand(t, []string{
		"keys", "bootstrap",
		"--repository-id", "repository-production",
		"--out-dir", bootstrapDirectory,
	})
	packagePath := filepath.Join(
		bootstrapDirectory,
		backupkeys.DeploymentKeyPackageCredentialName,
	)
	recoveryKeyPath := filepath.Join(
		bootstrapDirectory, "wukongim-backup-recovery.key",
	)

	stageDirectory := filepath.Join(root, "stage")
	stageOutput := runBackupKeysCommand(t, []string{
		"keys", "rotate", "stage",
		"--package", packagePath,
		"--recovery-key", recoveryKeyPath,
		"--out-dir", stageDirectory,
	})
	if !strings.Contains(stageOutput, `"revision":2`) {
		t.Fatalf("stage output = %q", stageOutput)
	}

	activateDirectory := filepath.Join(root, "activate")
	activateOutput := runBackupKeysCommand(t, []string{
		"keys", "rotate", "activate",
		"--package", filepath.Join(
			stageDirectory,
			backupkeys.DeploymentKeyPackageCredentialName,
		),
		"--recovery-key", recoveryKeyPath,
		"--out-dir", activateDirectory,
	})
	if !strings.Contains(activateOutput, `"revision":3`) {
		t.Fatalf("activate output = %q", activateOutput)
	}
}

func runBackupKeysCommand(t *testing.T, args []string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(%v): %v (stderr=%s)", args, err, stderr.String())
	}
	return stdout.String()
}
