package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/ssh"

	"github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerun"
)

const (
	accessCredentialSchemaV1  = "wukongim.chat_lifecycle.access/v1"
	encryptedAccessSchemaV1   = "wukongim.chat_lifecycle.encrypted_access/v1"
	accessEncryptionAlgorithm = "x25519-xsalsa20-poly1305-sealed-box"
)

var (
	accessIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)
	hex40Pattern          = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hex64Pattern          = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sha256DigestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	accessUserPattern     = regexp.MustCompile(`^operator-[0-9a-f]{24}$`)
)

// accessCredential is the short-lived plaintext operator handoff. It may only
// exist in the deployment process and one request-scoped local state directory.
type accessCredential struct {
	Schema               string `json:"schema"`
	RequestID            string `json:"request_id"`
	LeaseID              string `json:"lease_id"`
	SourceSHA            string `json:"source_sha"`
	DeploymentPlanDigest string `json:"deployment_plan_digest"`
	ManagerURL           string `json:"manager_url"`
	DemoURL              string `json:"demo_url"`
	Username             string `json:"username"`
	Password             string `json:"password"`
	LeaseExpiresAt       string `json:"lease_expires_at"`
}

// encryptedAccessEnvelope carries only request identity, recipient identity,
// and an authenticated sealed box. It is safe for bounded GitHub Artifacts.
type encryptedAccessEnvelope struct {
	Schema               string `json:"schema"`
	Algorithm            string `json:"algorithm"`
	RequestID            string `json:"request_id"`
	LeaseID              string `json:"lease_id"`
	SourceSHA            string `json:"source_sha"`
	DeploymentPlanDigest string `json:"deployment_plan_digest"`
	RecipientFingerprint string `json:"recipient_fingerprint"`
	CiphertextBase64     string `json:"ciphertext_base64"`
}

func addSealAccessCommand(root *cobra.Command) {
	var recipient string
	command := &cobra.Command{
		Use: "seal-access", Short: "Seal one UI credential for an exact Codex Ed25519 identity", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := io.ReadAll(io.LimitReader(command.InOrStdin(), maxInputBytes+1))
			if err != nil || len(body) == 0 || len(body) > maxInputBytes {
				return chatlifecyclerun.ErrInvalidInput
			}
			var credential accessCredential
			if err := decodeStrictJSON(body, &credential); err != nil || !validAccessCredential(credential) {
				return chatlifecyclerun.ErrInvalidInput
			}
			recipientKey, sshKey, err := parseEd25519PublicKey(recipient)
			if err != nil {
				return chatlifecyclerun.ErrInvalidInput
			}
			ciphertext, err := box.SealAnonymous(nil, body, recipientKey, rand.Reader)
			if err != nil {
				return chatlifecyclerun.ErrInvalidInput
			}
			envelope := encryptedAccessEnvelope{
				Schema: encryptedAccessSchemaV1, Algorithm: accessEncryptionAlgorithm,
				RequestID: credential.RequestID, LeaseID: credential.LeaseID, SourceSHA: credential.SourceSHA,
				DeploymentPlanDigest: credential.DeploymentPlanDigest,
				RecipientFingerprint: ssh.FingerprintSHA256(sshKey),
				CiphertextBase64:     base64.StdEncoding.EncodeToString(ciphertext),
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(envelope)
		},
	}
	command.Flags().StringVar(&recipient, "recipient", "", "exact OpenSSH Ed25519 recipient public key")
	if err := command.MarkFlagRequired("recipient"); err != nil {
		panic(err)
	}
	root.AddCommand(command)
}

func addOpenAccessCommand(root *cobra.Command) {
	var envelopePath, identityPath, requestID, nowValue, outputPath string
	command := &cobra.Command{
		Use: "open-access", Short: "Decrypt one exact UI credential into a new mode-0600 local file", Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			var envelope encryptedAccessEnvelope
			if err := readStrict(envelopePath, &envelope); err != nil || !validEncryptedAccessEnvelope(envelope) ||
				envelope.RequestID != requestID {
				return chatlifecyclerun.ErrInvalidInput
			}
			privateKey, publicKey, sshKey, err := readEd25519Identity(identityPath)
			if err != nil || ssh.FingerprintSHA256(sshKey) != envelope.RecipientFingerprint {
				return chatlifecyclerun.ErrInvalidInput
			}
			ciphertext, err := base64.StdEncoding.DecodeString(envelope.CiphertextBase64)
			if err != nil || len(ciphertext) == 0 || len(ciphertext) > maxInputBytes {
				return chatlifecyclerun.ErrInvalidInput
			}
			plaintext, ok := box.OpenAnonymous(nil, ciphertext, publicKey, privateKey)
			if !ok || len(plaintext) == 0 || len(plaintext) > maxInputBytes {
				return chatlifecyclerun.ErrInvalidInput
			}
			var credential accessCredential
			if err := decodeStrictJSON(plaintext, &credential); err != nil || !validAccessCredential(credential) ||
				credential.RequestID != envelope.RequestID || credential.LeaseID != envelope.LeaseID ||
				credential.SourceSHA != envelope.SourceSHA || credential.DeploymentPlanDigest != envelope.DeploymentPlanDigest {
				return chatlifecyclerun.ErrInvalidInput
			}
			now, err := time.Parse(time.RFC3339, nowValue)
			expiresAt, _ := time.Parse(time.RFC3339, credential.LeaseExpiresAt)
			if err != nil || now.Location() != time.UTC || !now.Before(expiresAt) {
				return chatlifecyclerun.ErrInvalidInput
			}
			return writePrivateAtomic(outputPath, plaintext)
		},
	}
	flags := command.Flags()
	flags.StringVar(&envelopePath, "envelope", "", "authenticated encrypted access Artifact")
	flags.StringVar(&identityPath, "identity", "", "request-scoped OpenSSH Ed25519 private key")
	flags.StringVar(&requestID, "request-id", "", "exact request identity")
	flags.StringVar(&nowValue, "now", "", "trusted current RFC3339 UTC time")
	flags.StringVar(&outputPath, "output", "", "new local plaintext credential file")
	for _, name := range []string{"envelope", "identity", "request-id", "now", "output"} {
		if err := command.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	root.AddCommand(command)
}

func validAccessCredential(credential accessCredential) bool {
	if credential.Schema != accessCredentialSchemaV1 || !accessIdentityPattern.MatchString(credential.RequestID) ||
		!accessIdentityPattern.MatchString(credential.LeaseID) || !hex40Pattern.MatchString(credential.SourceSHA) ||
		!sha256DigestPattern.MatchString(credential.DeploymentPlanDigest) || !accessUserPattern.MatchString(credential.Username) ||
		!hex64Pattern.MatchString(credential.Password) {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, credential.LeaseExpiresAt)
	if err != nil || expiresAt.Location() != time.UTC {
		return false
	}
	manager, managerOK := validAccessURL(credential.ManagerURL, "/")
	demo, demoOK := validAccessURL(credential.DemoURL, "/demo/")
	return managerOK && demoOK && manager.Scheme == demo.Scheme && manager.Host == demo.Host
}

func validEncryptedAccessEnvelope(envelope encryptedAccessEnvelope) bool {
	return envelope.Schema == encryptedAccessSchemaV1 && envelope.Algorithm == accessEncryptionAlgorithm &&
		accessIdentityPattern.MatchString(envelope.RequestID) && accessIdentityPattern.MatchString(envelope.LeaseID) &&
		hex40Pattern.MatchString(envelope.SourceSHA) && sha256DigestPattern.MatchString(envelope.DeploymentPlanDigest) &&
		strings.HasPrefix(envelope.RecipientFingerprint, "SHA256:") && len(envelope.RecipientFingerprint) <= 80 &&
		len(envelope.CiphertextBase64) > 0 && len(envelope.CiphertextBase64) <= 2*maxInputBytes
}

func validAccessURL(raw, exactPath string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.Path != exactPath ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.Port() != "" {
		return nil, false
	}
	if _, err := netip.ParseAddr(parsed.Hostname()); err != nil {
		return nil, false
	}
	return parsed, true
}

func parseEd25519PublicKey(raw string) (*[32]byte, ssh.PublicKey, error) {
	parsed, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(raw)))
	if err != nil || len(options) != 0 || strings.TrimSpace(string(rest)) != "" || parsed.Type() != ssh.KeyAlgoED25519 {
		return nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	cryptoKey, ok := parsed.(ssh.CryptoPublicKey)
	if !ok {
		return nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	public, ok := cryptoKey.CryptoPublicKey().(ed25519.PublicKey)
	if !ok || len(public) != ed25519.PublicKeySize {
		return nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	converted, ok := ed25519PublicToX25519(public)
	if !ok {
		return nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	return converted, parsed, nil
}

func readEd25519Identity(path string) (*[32]byte, *[32]byte, ssh.PublicKey, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 || len(body) > 64<<10 {
		return nil, nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	parsed, err := ssh.ParseRawPrivateKey(body)
	if err != nil {
		return nil, nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	var private ed25519.PrivateKey
	switch key := parsed.(type) {
	case ed25519.PrivateKey:
		private = key
	case *ed25519.PrivateKey:
		private = *key
	default:
		return nil, nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	if len(private) != ed25519.PrivateKeySize {
		return nil, nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	public := private.Public().(ed25519.PublicKey)
	convertedPublic, ok := ed25519PublicToX25519(public)
	if !ok {
		return nil, nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	digest := sha512.Sum512(private.Seed())
	convertedPrivate := new([32]byte)
	copy(convertedPrivate[:], digest[:32])
	convertedPrivate[0] &= 248
	convertedPrivate[31] &= 127
	convertedPrivate[31] |= 64
	sshKey, err := ssh.NewPublicKey(public)
	if err != nil {
		return nil, nil, nil, chatlifecyclerun.ErrInvalidInput
	}
	return convertedPrivate, convertedPublic, sshKey, nil
}

// ed25519PublicToX25519 maps the Edwards y coordinate to the Montgomery u
// coordinate using u=(1+y)/(1-y) over 2^255-19.
func ed25519PublicToX25519(public ed25519.PublicKey) (*[32]byte, bool) {
	if len(public) != ed25519.PublicKeySize {
		return nil, false
	}
	littleEndianY := append([]byte(nil), public...)
	littleEndianY[31] &= 0x7f
	bigEndianY := make([]byte, len(littleEndianY))
	for index := range littleEndianY {
		bigEndianY[len(littleEndianY)-1-index] = littleEndianY[index]
	}
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	y := new(big.Int).SetBytes(bigEndianY)
	if y.Cmp(p) >= 0 {
		return nil, false
	}
	one := big.NewInt(1)
	numerator := new(big.Int).Add(one, y)
	numerator.Mod(numerator, p)
	denominator := new(big.Int).Sub(one, y)
	denominator.Mod(denominator, p)
	inverse := new(big.Int).ModInverse(denominator, p)
	if inverse == nil {
		return nil, false
	}
	u := new(big.Int).Mul(numerator, inverse)
	u.Mod(u, p)
	bigEndianU := u.Bytes()
	converted := new([32]byte)
	for index := range bigEndianU {
		converted[index] = bigEndianU[len(bigEndianU)-1-index]
	}
	probeScalar := make([]byte, curve25519.ScalarSize)
	probeScalar[0] = 1
	if _, err := curve25519.X25519(probeScalar, converted[:]); err != nil {
		return nil, false
	}
	return converted, true
}

func decodeStrictJSON(body []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return chatlifecyclerun.ErrInvalidInput
	}
	return nil
}
