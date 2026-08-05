package chatlifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCredentialsUsesEnvironmentOrProtectedFilesWithoutDisclosure(t *testing.T) {
	const (
		benchSecret  = "bench-secret-sentinel"
		workerSecret = "worker-secret-sentinel"
	)
	t.Run("environment", func(t *testing.T) {
		t.Setenv(benchTokenEnvironment, benchSecret)
		t.Setenv(workerTokenEnvironment, workerSecret)
		credentials, err := LoadCredentials()
		if err != nil {
			t.Fatal(err)
		}
		if credentials.BenchToken() != benchSecret || credentials.WorkerToken() != workerSecret {
			t.Fatal("environment credentials did not load")
		}
		assertCredentialsRedacted(t, credentials, benchSecret, workerSecret)
	})

	t.Run("files", func(t *testing.T) {
		benchPath := writeCredentialFile(t, benchSecret)
		workerPath := writeCredentialFile(t, workerSecret)
		t.Setenv(benchTokenFileEnvironment, benchPath)
		t.Setenv(workerTokenFileEnvironment, workerPath)
		credentials, err := LoadCredentials()
		if err != nil {
			t.Fatal(err)
		}
		if credentials.BenchToken() != benchSecret || credentials.WorkerToken() != workerSecret {
			t.Fatal("file credentials did not load")
		}
		assertCredentialsRedacted(t, credentials, benchSecret, workerSecret)
	})
}

func TestLoadCredentialsRejectsMissingAmbiguousAndUnsafeFilesWithoutValues(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		_, err := LoadCredentials()
		if err == nil || !strings.Contains(err.Error(), benchTokenEnvironment) {
			t.Fatalf("missing credentials error = %v", err)
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		const secret = "ambiguous-secret-sentinel"
		t.Setenv(benchTokenEnvironment, secret)
		t.Setenv(benchTokenFileEnvironment, writeCredentialFile(t, secret))
		t.Setenv(workerTokenEnvironment, "worker-secret")
		_, err := LoadCredentials()
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("ambiguous credentials error = %v", err)
		}
	})
	t.Run("unsafe file mode", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bench-token")
		if err := os.WriteFile(path, []byte("unsafe-secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv(benchTokenFileEnvironment, path)
		t.Setenv(workerTokenEnvironment, "worker-secret")
		_, err := LoadCredentials()
		if err == nil || strings.Contains(err.Error(), "unsafe-secret") {
			t.Fatalf("unsafe credentials error = %v", err)
		}
	})
}

func writeCredentialFile(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertCredentialsRedacted(t *testing.T, credentials Credentials, secrets ...string) {
	t.Helper()
	encoded, err := json.Marshal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	outputs := []string{fmt.Sprintf("%v", credentials), fmt.Sprintf("%+v", credentials), fmt.Sprintf("%#v", credentials), string(encoded)}
	for _, output := range outputs {
		if !strings.Contains(output, "[REDACTED]") {
			t.Fatalf("credential rendering was not redacted: %q", output)
		}
		for _, secret := range secrets {
			if strings.Contains(output, secret) {
				t.Fatalf("credential rendering leaked secret: %q", output)
			}
		}
	}
}
