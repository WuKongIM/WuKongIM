package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/ssh"

	"github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerun"
)

const (
	deploymentIdentityPayloadSchemaV1   = "wukongim.chat_lifecycle.deployment_identity/v1"
	encryptedDeploymentIdentitySchemaV1 = "wukongim.chat_lifecycle.encrypted_deployment_identity/v1"
)

type deploymentIdentityPayload struct {
	Schema                string `json:"schema"`
	RequestID             string `json:"request_id"`
	LeaseID               string `json:"lease_id"`
	SourceSHA             string `json:"source_sha"`
	PlanDigest            string `json:"plan_digest"`
	DeploymentPublicKey   string `json:"deployment_public_key"`
	DeploymentFingerprint string `json:"deployment_fingerprint"`
	ExpiresAt             string `json:"expires_at"`
	PrivateKeyBase64      string `json:"private_key_base64"`
}

type encryptedDeploymentIdentityEnvelope struct {
	Schema                string `json:"schema"`
	Algorithm             string `json:"algorithm"`
	RequestID             string `json:"request_id"`
	LeaseID               string `json:"lease_id"`
	SourceSHA             string `json:"source_sha"`
	PlanDigest            string `json:"plan_digest"`
	RecipientFingerprint  string `json:"recipient_fingerprint"`
	DeploymentPublicKey   string `json:"deployment_public_key"`
	DeploymentFingerprint string `json:"deployment_fingerprint"`
	ExpiresAt             string `json:"expires_at"`
	CiphertextBase64      string `json:"ciphertext_base64"`
}

type deploymentIdentityOptions struct {
	recipient, identity, envelope, requestID, leaseID string
	sourceSHA, planDigest, expiresAt, now, output     string
}

func addDeploymentIdentityCommands(root *cobra.Command) {
	addSealDeploymentIdentityCommand(root)
	addOpenDeploymentIdentityCommand(root)
}

func addSealDeploymentIdentityCommand(root *cobra.Command) {
	var options deploymentIdentityOptions
	command := &cobra.Command{
		Use: "seal-deployment-identity", Short: "Seal one Lease-scoped deployment SSH identity", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if !validDeploymentIdentityMetadata(options.requestID, options.leaseID, options.sourceSHA, options.planDigest, options.expiresAt) {
				return chatlifecyclerun.ErrInvalidInput
			}
			recipientKey, recipientSSH, err := parseEd25519PublicKey(options.recipient)
			if err != nil {
				return chatlifecyclerun.ErrInvalidInput
			}
			privateBody, deploymentSSH, err := readPrivateIdentityBody(options.identity)
			if err != nil {
				return chatlifecyclerun.ErrInvalidInput
			}
			deploymentPublic := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(deploymentSSH)))
			deploymentFingerprint := ssh.FingerprintSHA256(deploymentSSH)
			payload := deploymentIdentityPayload{
				Schema: deploymentIdentityPayloadSchemaV1, RequestID: options.requestID, LeaseID: options.leaseID,
				SourceSHA: options.sourceSHA, PlanDigest: options.planDigest, DeploymentPublicKey: deploymentPublic,
				DeploymentFingerprint: deploymentFingerprint, ExpiresAt: options.expiresAt,
				PrivateKeyBase64: base64.StdEncoding.EncodeToString(privateBody),
			}
			plaintext, err := json.Marshal(payload)
			if err != nil || len(plaintext) > maxInputBytes {
				return chatlifecyclerun.ErrInvalidInput
			}
			ciphertext, err := box.SealAnonymous(nil, plaintext, recipientKey, rand.Reader)
			if err != nil {
				return chatlifecyclerun.ErrInvalidInput
			}
			envelope := encryptedDeploymentIdentityEnvelope{
				Schema: encryptedDeploymentIdentitySchemaV1, Algorithm: accessEncryptionAlgorithm,
				RequestID: options.requestID, LeaseID: options.leaseID, SourceSHA: options.sourceSHA,
				PlanDigest: options.planDigest, RecipientFingerprint: ssh.FingerprintSHA256(recipientSSH),
				DeploymentPublicKey: deploymentPublic, DeploymentFingerprint: deploymentFingerprint, ExpiresAt: options.expiresAt,
				CiphertextBase64: base64.StdEncoding.EncodeToString(ciphertext),
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(envelope)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.recipient, "recipient", "", "exact wrapping-key OpenSSH Ed25519 public key")
	flags.StringVar(&options.identity, "identity", "", "mode-0600 Lease deployment OpenSSH Ed25519 private key")
	flags.StringVar(&options.requestID, "request-id", "", "exact request identity")
	flags.StringVar(&options.leaseID, "lease-id", "", "exact Lease identity")
	flags.StringVar(&options.sourceSHA, "source-sha", "", "exact source commit")
	flags.StringVar(&options.planDigest, "plan-digest", "", "exact Cloud Lease Plan digest")
	flags.StringVar(&options.expiresAt, "expires-at", "", "immutable Lease expiry")
	markRequired(command, "recipient", "identity", "request-id", "lease-id", "source-sha", "plan-digest", "expires-at")
	root.AddCommand(command)
}

func addOpenDeploymentIdentityCommand(root *cobra.Command) {
	var options deploymentIdentityOptions
	command := &cobra.Command{
		Use: "open-deployment-identity", Short: "Open one unexpired exact Lease deployment SSH identity", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			var envelope encryptedDeploymentIdentityEnvelope
			if err := readStrict(options.envelope, &envelope); err != nil || !validEncryptedDeploymentIdentityEnvelope(envelope) ||
				envelope.RequestID != options.requestID || envelope.LeaseID != options.leaseID ||
				envelope.SourceSHA != options.sourceSHA || envelope.PlanDigest != options.planDigest {
				return chatlifecyclerun.ErrInvalidInput
			}
			now, err := time.Parse(time.RFC3339, options.now)
			if err != nil || now.Location() != time.UTC {
				return chatlifecyclerun.ErrInvalidInput
			}
			expiresAt, _ := time.Parse(time.RFC3339, envelope.ExpiresAt)
			if !now.Before(expiresAt) {
				return chatlifecyclerun.ErrInvalidInput
			}
			wrappingPrivate, wrappingPublic, wrappingSSH, err := readEd25519Identity(options.identity)
			if err != nil || ssh.FingerprintSHA256(wrappingSSH) != envelope.RecipientFingerprint {
				return chatlifecyclerun.ErrInvalidInput
			}
			ciphertext, err := base64.StdEncoding.DecodeString(envelope.CiphertextBase64)
			if err != nil || len(ciphertext) == 0 || len(ciphertext) > maxInputBytes {
				return chatlifecyclerun.ErrInvalidInput
			}
			plaintext, ok := box.OpenAnonymous(nil, ciphertext, wrappingPublic, wrappingPrivate)
			if !ok || len(plaintext) == 0 || len(plaintext) > maxInputBytes {
				return chatlifecyclerun.ErrInvalidInput
			}
			var payload deploymentIdentityPayload
			if err := decodeStrictJSON(plaintext, &payload); err != nil || !payloadMatchesEnvelope(payload, envelope) {
				return chatlifecyclerun.ErrInvalidInput
			}
			privateBody, err := base64.StdEncoding.DecodeString(payload.PrivateKeyBase64)
			if err != nil || len(privateBody) == 0 || len(privateBody) > 64<<10 {
				return chatlifecyclerun.ErrInvalidInput
			}
			deploymentSSH, err := sshPublicFromPrivateBody(privateBody)
			if err != nil || ssh.FingerprintSHA256(deploymentSSH) != envelope.DeploymentFingerprint ||
				strings.TrimSpace(string(ssh.MarshalAuthorizedKey(deploymentSSH))) != payload.DeploymentPublicKey {
				return chatlifecyclerun.ErrInvalidInput
			}
			return writePrivateAtomic(options.output, privateBody)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.envelope, "envelope", "", "authenticated encrypted deployment identity envelope")
	flags.StringVar(&options.identity, "identity", "", "mode-0600 wrapping-key OpenSSH Ed25519 private key")
	flags.StringVar(&options.requestID, "request-id", "", "exact request identity")
	flags.StringVar(&options.leaseID, "lease-id", "", "exact Lease identity")
	flags.StringVar(&options.sourceSHA, "source-sha", "", "exact source commit")
	flags.StringVar(&options.planDigest, "plan-digest", "", "exact Cloud Lease Plan digest")
	flags.StringVar(&options.now, "now", "", "trusted current RFC3339 UTC time")
	flags.StringVar(&options.output, "output", "", "new mode-0600 deployment private key")
	markRequired(command, "envelope", "identity", "request-id", "lease-id", "source-sha", "plan-digest", "now", "output")
	root.AddCommand(command)
}

func markRequired(command *cobra.Command, names ...string) {
	for _, name := range names {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
}

func validDeploymentIdentityMetadata(requestID, leaseID, sourceSHA, planDigest, expiresAt string) bool {
	if !accessIdentityPattern.MatchString(requestID) || !accessIdentityPattern.MatchString(leaseID) ||
		!hex40Pattern.MatchString(sourceSHA) || !hex64Pattern.MatchString(planDigest) {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	return err == nil && parsed.Location() == time.UTC
}

func validEncryptedDeploymentIdentityEnvelope(envelope encryptedDeploymentIdentityEnvelope) bool {
	return envelope.Schema == encryptedDeploymentIdentitySchemaV1 && envelope.Algorithm == accessEncryptionAlgorithm &&
		validDeploymentIdentityMetadata(envelope.RequestID, envelope.LeaseID, envelope.SourceSHA, envelope.PlanDigest, envelope.ExpiresAt) &&
		strings.HasPrefix(envelope.RecipientFingerprint, "SHA256:") && len(envelope.RecipientFingerprint) <= 80 &&
		strings.HasPrefix(envelope.DeploymentPublicKey, ssh.KeyAlgoED25519+" ") && len(envelope.DeploymentPublicKey) <= 256 &&
		strings.HasPrefix(envelope.DeploymentFingerprint, "SHA256:") && len(envelope.DeploymentFingerprint) <= 80 &&
		len(envelope.CiphertextBase64) > 0 && len(envelope.CiphertextBase64) <= 2*maxInputBytes
}

func payloadMatchesEnvelope(payload deploymentIdentityPayload, envelope encryptedDeploymentIdentityEnvelope) bool {
	return payload.Schema == deploymentIdentityPayloadSchemaV1 && payload.RequestID == envelope.RequestID &&
		payload.LeaseID == envelope.LeaseID && payload.SourceSHA == envelope.SourceSHA &&
		payload.PlanDigest == envelope.PlanDigest && payload.ExpiresAt == envelope.ExpiresAt &&
		payload.DeploymentFingerprint == envelope.DeploymentFingerprint &&
		payload.DeploymentPublicKey == envelope.DeploymentPublicKey && len(payload.PrivateKeyBase64) > 0
}

func readPrivateIdentityBody(path string) ([]byte, ssh.PublicKey, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 || len(body) > 64<<10 {
		return nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	publicKey, err := sshPublicFromPrivateBody(body)
	if err != nil {
		return nil, nil, err
	}
	return body, publicKey, nil
}

func sshPublicFromPrivateBody(body []byte) (ssh.PublicKey, error) {
	parsed, err := ssh.ParseRawPrivateKey(body)
	if err != nil {
		return nil, chatlifecyclerun.ErrInvalidInput
	}
	var private ed25519.PrivateKey
	switch key := parsed.(type) {
	case ed25519.PrivateKey:
		private = key
	case *ed25519.PrivateKey:
		private = *key
	default:
		return nil, chatlifecyclerun.ErrInvalidInput
	}
	if len(private) != ed25519.PrivateKeySize {
		return nil, chatlifecyclerun.ErrInvalidInput
	}
	publicKey, err := ssh.NewPublicKey(private.Public())
	if err != nil {
		return nil, chatlifecyclerun.ErrInvalidInput
	}
	return publicKey, nil
}
