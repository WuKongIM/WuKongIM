package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	goldenPathReceiptLimit                 = 16 << 10
	goldenPathReceiptSchema                = "wukongim.docs.golden-path-verification/v1"
	goldenPathReceiptResult                = "passed"
	goldenPathRequiredScenario             = "javascript-web-quickstart/alice-bob-reconnect-sync/v1"
	goldenPathRequiredSDKPackage           = "wukongimjssdk"
	goldenPathRequiredSDKVersion           = "1.3.5"
	goldenPathRequiredNodeVersion          = "22.12.0"
	goldenPathRequiredBrowserEngine        = "chromium"
	goldenPathRequiredPlaywrightPackage    = "@playwright/test"
	goldenPathRequiredPlaywrightVersion    = "1.62.1"
	goldenPathRequiredChromiumRevision     = "1234"
	goldenPathRequiredBrowserVersion       = "151.0.7922.34"
	goldenPathReceiptMalformedError        = "golden-path attestation is malformed"
	goldenPathReceiptMismatchError         = "golden-path attestation does not match the pinned documentation snapshot"
	goldenPathReceiptFileBoundaryError     = "golden-path attestation must be a non-empty regular file no larger than 16 KiB"
	goldenPathReceiptChangedWhileReadError = "golden-path attestation changed while it was being read"
	goldenPathReceiptRelativeDirectory     = "tmp/docs-site-e2e"
	goldenPathReceiptFilename              = "golden-path.json"
)

type goldenPathReceiptPackage struct {
	Package string `json:"package"`
	Version string `json:"version"`
}

type goldenPathReceiptBrowser struct {
	Engine            string `json:"engine"`
	PlaywrightPackage string `json:"playwright_package"`
	PlaywrightVersion string `json:"playwright_version"`
	Revision          string `json:"revision"`
	BrowserVersion    string `json:"browser_version"`
}

type goldenPathReceiptSample struct {
	Scenario          string `json:"scenario"`
	PackageLockSHA256 string `json:"package_lock_sha256"`
}

type goldenPathReceiptRuntime struct {
	Node    string                   `json:"node"`
	Browser goldenPathReceiptBrowser `json:"browser"`
}

type goldenPathReceipt struct {
	Schema         string                   `json:"schema"`
	Result         string                   `json:"result"`
	SourceRevision string                   `json:"source_revision"`
	Sample         goldenPathReceiptSample  `json:"sample"`
	SDK            goldenPathReceiptPackage `json:"sdk"`
	Runtime        goldenPathReceiptRuntime `json:"runtime"`
}

type goldenPathAttestationSummary struct {
	SourceRevision    string
	PackageLockSHA256 string
}

func newDocumentationIntegrationReceiptOutput(root string) (string, string, error) {
	artifactRoot := filepath.Join(root, filepath.FromSlash(goldenPathReceiptRelativeDirectory))
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		return "", "", fmt.Errorf("create documentation integration receipt root: %w", err)
	}
	directory, err := os.MkdirTemp(
		artifactRoot,
		fmt.Sprintf("docs-integration-receipt-%d-", time.Now().UnixNano()),
	)
	if err != nil {
		return "", "", fmt.Errorf("create documentation integration receipt directory: %w", err)
	}
	return directory, filepath.Join(directory, goldenPathReceiptFilename), nil
}

func readAndValidateGoldenPathAttestation(
	ctx context.Context,
	commands reviewCommandExecutor,
	root string,
	receiptPath string,
) (goldenPathAttestationSummary, error) {
	data, err := readBoundedGoldenPathAttestation(receiptPath)
	if err != nil {
		return goldenPathAttestationSummary{}, err
	}
	revision, err := reviewGitOutput(
		ctx, commands, root, "rev-parse", "--verify", "HEAD",
	)
	if err != nil || !isLowerHexDigest(revision, 40, 64) {
		return goldenPathAttestationSummary{}, errors.New("inspect documentation integration source revision")
	}
	lockfile, err := os.ReadFile(filepath.Join(
		root,
		"docs-site",
		"examples",
		"javascript-web-quickstart",
		"package-lock.json",
	))
	if err != nil {
		return goldenPathAttestationSummary{}, errors.New("read documentation integration package lock")
	}
	lockHash := sha256.Sum256(lockfile)
	return validateGoldenPathAttestation(data, revision, hex.EncodeToString(lockHash[:]))
}

func readBoundedGoldenPathAttestation(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > goldenPathReceiptLimit {
		return nil, errors.New(goldenPathReceiptFileBoundaryError)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New(goldenPathReceiptFileBoundaryError)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, goldenPathReceiptLimit+1))
	if err != nil {
		return nil, errors.New("read golden-path attestation")
	}
	if len(data) == 0 || len(data) > goldenPathReceiptLimit {
		return nil, errors.New(goldenPathReceiptFileBoundaryError)
	}
	if int64(len(data)) != info.Size() {
		return nil, errors.New(goldenPathReceiptChangedWhileReadError)
	}
	return data, nil
}

func validateGoldenPathAttestation(
	data []byte,
	expectedRevision string,
	expectedLockSHA string,
) (goldenPathAttestationSummary, error) {
	if err := validateGoldenPathReceiptShape(data); err != nil {
		return goldenPathAttestationSummary{}, errors.New(goldenPathReceiptMalformedError)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt goldenPathReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return goldenPathAttestationSummary{}, errors.New(goldenPathReceiptMalformedError)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return goldenPathAttestationSummary{}, errors.New(goldenPathReceiptMalformedError)
	}
	if !isLowerHexDigest(receipt.SourceRevision, 40, 64) ||
		!isLowerHexDigest(receipt.Sample.PackageLockSHA256, 64) ||
		receipt.Schema != goldenPathReceiptSchema ||
		receipt.Result != goldenPathReceiptResult ||
		receipt.SourceRevision != expectedRevision ||
		receipt.Sample.Scenario != goldenPathRequiredScenario ||
		receipt.Sample.PackageLockSHA256 != expectedLockSHA ||
		receipt.SDK.Package != goldenPathRequiredSDKPackage ||
		receipt.SDK.Version != goldenPathRequiredSDKVersion ||
		receipt.Runtime.Node != goldenPathRequiredNodeVersion ||
		receipt.Runtime.Browser.Engine != goldenPathRequiredBrowserEngine ||
		receipt.Runtime.Browser.PlaywrightPackage != goldenPathRequiredPlaywrightPackage ||
		receipt.Runtime.Browser.PlaywrightVersion != goldenPathRequiredPlaywrightVersion ||
		receipt.Runtime.Browser.Revision != goldenPathRequiredChromiumRevision ||
		receipt.Runtime.Browser.BrowserVersion != goldenPathRequiredBrowserVersion {
		return goldenPathAttestationSummary{}, errors.New(goldenPathReceiptMismatchError)
	}
	return goldenPathAttestationSummary{
		SourceRevision:    receipt.SourceRevision,
		PackageLockSHA256: receipt.Sample.PackageLockSHA256,
	}, nil
}

func validateGoldenPathReceiptShape(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	top, err := decodeExactJSONObject(data, "schema", "result", "source_revision", "sample", "sdk", "runtime")
	if err != nil {
		return err
	}
	if _, err := decodeExactJSONObject(top["sample"], "scenario", "package_lock_sha256"); err != nil {
		return err
	}
	if _, err := decodeExactJSONObject(top["sdk"], "package", "version"); err != nil {
		return err
	}
	runtime, err := decodeExactJSONObject(top["runtime"], "node", "browser")
	if err != nil {
		return err
	}
	_, err = decodeExactJSONObject(
		runtime["browser"],
		"engine",
		"playwright_package",
		"playwright_version",
		"revision",
		"browser_version",
	)
	return err
}

func decodeExactJSONObject(data []byte, expectedKeys ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, errors.New("expected JSON object")
	}
	if err := requireJSONEnd(decoder); err != nil {
		return nil, err
	}
	if len(object) != len(expectedKeys) {
		return nil, errors.New("unexpected JSON object keys")
	}
	for _, key := range expectedKeys {
		if _, ok := object[key]; !ok {
			return nil, errors.New("unexpected JSON object keys")
		}
	}
	return object, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walkValue func() error
	walkValue = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, duplicate := seen[key]; duplicate {
					return errors.New("duplicate JSON object key")
				}
				seen[key] = struct{}{}
				if err := walkValue(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walkValue(); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		end, err := decoder.Token()
		if err != nil || end != matchingJSONDelimiter(delimiter) {
			return errors.New("malformed JSON delimiter")
		}
		return nil
	}
	if err := walkValue(); err != nil {
		return err
	}
	return requireJSONEnd(decoder)
}

func matchingJSONDelimiter(delimiter json.Delim) json.Delim {
	if delimiter == '{' {
		return '}'
	}
	return ']'
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func reviewGitOutput(
	ctx context.Context,
	commands reviewCommandExecutor,
	root string,
	arguments ...string,
) (string, error) {
	commandArguments := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
	}, arguments...)
	output, err := commands.Output(ctx, checkStep{
		directory: root,
		name:      "git",
		arguments: commandArguments,
	})
	if err != nil {
		return "", errors.New("inspect documentation integration source identity")
	}
	return strings.TrimSpace(string(output)), nil
}

func isLowerHexDigest(value string, lengths ...int) bool {
	validLength := false
	for _, length := range lengths {
		if len(value) == length {
			validLength = true
			break
		}
	}
	if !validLength || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == len(value)
}

func abbreviateDigest(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
