package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestAccessEnvelopeRoundTripKeepsCredentialsEncryptedAndPrivate(t *testing.T) {
	publicKey, privateKeyPath := accessTestKey(t)
	credential := accessCredential{
		Schema: accessCredentialSchemaV1, RequestID: "chat-request-1", LeaseID: "lease-1",
		SourceSHA: strings.Repeat("a", 40), DeploymentPlanDigest: strings.Repeat("b", 64),
		ManagerURL: "http://203.0.113.10/", DemoURL: "http://203.0.113.10/demo/",
		Username: "operator-0123456789abcdef01234567", Password: strings.Repeat("c", 64),
		LeaseExpiresAt: "2030-01-02T03:04:05Z",
	}
	plaintext, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}

	var sealed bytes.Buffer
	command := newRootCommand(&sealed)
	command.SetIn(bytes.NewReader(plaintext))
	command.SetArgs([]string{"seal-access", "--recipient", publicKey})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed.Bytes(), []byte(credential.Username)) || bytes.Contains(sealed.Bytes(), []byte(credential.Password)) {
		t.Fatal("encrypted access envelope contains plaintext credentials")
	}
	var envelope encryptedAccessEnvelope
	if err := json.Unmarshal(sealed.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Schema != encryptedAccessSchemaV1 || envelope.RequestID != credential.RequestID ||
		envelope.Algorithm != accessEncryptionAlgorithm || !strings.HasPrefix(envelope.RecipientFingerprint, "SHA256:") {
		t.Fatalf("envelope = %+v", envelope)
	}

	directory := t.TempDir()
	envelopePath := filepath.Join(directory, "encrypted-access.json")
	outputPath := filepath.Join(directory, "access.json")
	if err := os.WriteFile(envelopePath, sealed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	command = newRootCommand(&bytes.Buffer{})
	command.SetArgs([]string{"open-access", "--envelope", envelopePath, "--identity", privateKeyPath,
		"--request-id", credential.RequestID, "--output", outputPath})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("decrypted access mode = %o, want 600", info.Mode().Perm())
	}
	var opened accessCredential
	if err := readStrict(outputPath, &opened); err != nil {
		t.Fatal(err)
	}
	if opened != credential {
		t.Fatalf("opened credential = %+v, want %+v", opened, credential)
	}
}

func TestOpenAccessRejectsWrongIdentityRequestAndExistingOutput(t *testing.T) {
	publicKey, privateKeyPath := accessTestKey(t)
	_, wrongPrivateKeyPath := accessTestKey(t)
	credential := accessCredential{
		Schema: accessCredentialSchemaV1, RequestID: "request-safe", LeaseID: "lease-safe",
		SourceSHA: strings.Repeat("d", 40), DeploymentPlanDigest: strings.Repeat("e", 64),
		ManagerURL: "http://198.51.100.8/", DemoURL: "http://198.51.100.8/demo/",
		Username: "operator-abcdef0123456789abcdef01", Password: strings.Repeat("f", 64),
		LeaseExpiresAt: "2030-01-02T03:04:05Z",
	}
	body, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	var sealed bytes.Buffer
	command := newRootCommand(&sealed)
	command.SetIn(bytes.NewReader(body))
	command.SetArgs([]string{"seal-access", "--recipient", publicKey})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	envelopePath := filepath.Join(directory, "encrypted-access.json")
	if err := os.WriteFile(envelopePath, sealed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, identityRequest := range map[string][2]string{
		"wrong identity": {wrongPrivateKeyPath, credential.RequestID},
		"wrong request":  {privateKeyPath, "another-request"},
	} {
		t.Run(name, func(t *testing.T) {
			identity, request := identityRequest[0], identityRequest[1]
			outputPath := filepath.Join(directory, strings.ReplaceAll(name, " ", "-")+".json")
			command := newRootCommand(&bytes.Buffer{})
			command.SetArgs([]string{"open-access", "--envelope", envelopePath, "--identity", identity,
				"--request-id", request, "--output", outputPath})
			if err := command.Execute(); err == nil {
				t.Fatal("open-access accepted mismatched identity")
			}
			if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
				t.Fatalf("failed open created output: %v", err)
			}
		})
	}

	existing := filepath.Join(directory, "existing.json")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	command = newRootCommand(&bytes.Buffer{})
	command.SetArgs([]string{"open-access", "--envelope", envelopePath, "--identity", privateKeyPath,
		"--request-id", credential.RequestID, "--output", existing})
	if err := command.Execute(); err == nil {
		t.Fatal("open-access overwrote an existing output")
	}
	if body, err := os.ReadFile(existing); err != nil || string(body) != "keep" {
		t.Fatalf("existing output changed: %q, %v", body, err)
	}
}

func TestAccessRecipientRejectsLowOrderEd25519PublicKey(t *testing.T) {
	lowOrder, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseEd25519PublicKey(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(lowOrder)))); err == nil {
		t.Fatal("low-order Ed25519 recipient was accepted for sealed credentials")
	}
}

func accessTestKey(t *testing.T) (string, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "chat-lifecycle-test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))), path
}
