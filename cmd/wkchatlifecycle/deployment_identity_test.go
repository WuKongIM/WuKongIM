package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploymentIdentityEnvelopeRoundTripIsLeaseBoundAndPrivate(t *testing.T) {
	wrappingPublic, wrappingPrivate := accessTestKey(t)
	deploymentPublic, deploymentPrivate := accessTestKey(t)
	directory := t.TempDir()
	envelopePath := filepath.Join(directory, "encrypted-deployment-identity.json")
	openedPath := filepath.Join(directory, "deployment-key")

	var sealed bytes.Buffer
	command := newRootCommand(&sealed)
	command.SetArgs([]string{
		"seal-deployment-identity",
		"--recipient", wrappingPublic,
		"--identity", deploymentPrivate,
		"--request-id", "chat-request-1",
		"--lease-id", "chat-request-1-rehearsal-1",
		"--source-sha", strings.Repeat("a", 40),
		"--plan-digest", strings.Repeat("b", 64),
		"--expires-at", "2030-01-02T03:04:05Z",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	privateBody, err := os.ReadFile(deploymentPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed.Bytes(), privateBody) {
		t.Fatal("encrypted deployment identity contains plaintext key material")
	}
	var envelope encryptedDeploymentIdentityEnvelope
	if err := json.Unmarshal(sealed.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Schema != encryptedDeploymentIdentitySchemaV1 || envelope.LeaseID != "chat-request-1-rehearsal-1" ||
		envelope.DeploymentPublicKey != deploymentPublic || envelope.DeploymentFingerprint == "" || envelope.RecipientFingerprint == "" {
		t.Fatalf("envelope = %+v", envelope)
	}
	if err := os.WriteFile(envelopePath, sealed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	command = newRootCommand(&bytes.Buffer{})
	command.SetArgs([]string{
		"open-deployment-identity",
		"--envelope", envelopePath,
		"--identity", wrappingPrivate,
		"--request-id", "chat-request-1",
		"--lease-id", "chat-request-1-rehearsal-1",
		"--source-sha", strings.Repeat("a", 40),
		"--plan-digest", strings.Repeat("b", 64),
		"--now", "2030-01-02T02:04:05Z",
		"--output", openedPath,
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	openedBody, err := os.ReadFile(openedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(openedBody, privateBody) {
		t.Fatal("opened deployment identity differs from the sealed identity")
	}
	info, err := os.Stat(openedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("opened deployment identity mode = %o, want 600", info.Mode().Perm())
	}
}

func TestOpenDeploymentIdentityRejectsMismatchExpirationAndExistingOutput(t *testing.T) {
	wrappingPublic, wrappingPrivate := accessTestKey(t)
	_, wrongWrappingPrivate := accessTestKey(t)
	_, deploymentPrivate := accessTestKey(t)
	directory := t.TempDir()
	envelopePath := filepath.Join(directory, "encrypted-deployment-identity.json")

	var sealed bytes.Buffer
	command := newRootCommand(&sealed)
	command.SetArgs([]string{
		"seal-deployment-identity", "--recipient", wrappingPublic, "--identity", deploymentPrivate,
		"--request-id", "request-safe", "--lease-id", "lease-safe",
		"--source-sha", strings.Repeat("c", 40), "--plan-digest", strings.Repeat("d", 64),
		"--expires-at", "2030-01-02T03:04:05Z",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envelopePath, sealed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		identity  string
		requestID string
		leaseID   string
		now       string
	}{
		{name: "wrong wrapping identity", identity: wrongWrappingPrivate, requestID: "request-safe", leaseID: "lease-safe", now: "2030-01-02T02:04:05Z"},
		{name: "wrong request", identity: wrappingPrivate, requestID: "request-other", leaseID: "lease-safe", now: "2030-01-02T02:04:05Z"},
		{name: "wrong lease", identity: wrappingPrivate, requestID: "request-safe", leaseID: "lease-other", now: "2030-01-02T02:04:05Z"},
		{name: "expired", identity: wrappingPrivate, requestID: "request-safe", leaseID: "lease-safe", now: "2030-01-02T03:04:05Z"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(directory, strings.ReplaceAll(test.name, " ", "-"))
			command := newRootCommand(&bytes.Buffer{})
			command.SetArgs([]string{
				"open-deployment-identity", "--envelope", envelopePath, "--identity", test.identity,
				"--request-id", test.requestID, "--lease-id", test.leaseID,
				"--source-sha", strings.Repeat("c", 40), "--plan-digest", strings.Repeat("d", 64),
				"--now", test.now, "--output", output,
			})
			if err := command.Execute(); err == nil {
				t.Fatal("open-deployment-identity accepted invalid identity metadata")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("failed open created output: %v", err)
			}
		})
	}

	existing := filepath.Join(directory, "existing")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	command = newRootCommand(&bytes.Buffer{})
	command.SetArgs([]string{
		"open-deployment-identity", "--envelope", envelopePath, "--identity", wrappingPrivate,
		"--request-id", "request-safe", "--lease-id", "lease-safe",
		"--source-sha", strings.Repeat("c", 40), "--plan-digest", strings.Repeat("d", 64),
		"--now", "2030-01-02T02:04:05Z", "--output", existing,
	})
	if err := command.Execute(); err == nil {
		t.Fatal("open-deployment-identity overwrote an existing output")
	}
	if body, err := os.ReadFile(existing); err != nil || string(body) != "keep" {
		t.Fatalf("existing output changed: %q, %v", body, err)
	}
}
