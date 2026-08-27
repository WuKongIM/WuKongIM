//go:build e2e

package javascript_web_quickstart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	goldenPathAttestationSchema   = "wukongim.docs.golden-path-verification/v1"
	goldenPathScenario            = "javascript-web-quickstart/alice-bob-reconnect-sync/v1"
	goldenPathSDKPackage          = "wukongimjssdk"
	goldenPathSDKVersion          = "1.3.5"
	goldenPathNodeVersion         = "22.12.0"
	goldenPathPlaywrightPackage   = "@playwright/test"
	goldenPathPlaywrightVersion   = "1.62.1"
	goldenPathChromiumRevision    = "1234"
	goldenPathChromiumVersion     = "151.0.7922.34"
	goldenPathAttestationOutput   = "WK_DOCS_GOLDEN_PATH_ATTESTATION_OUTPUT"
	goldenPathRuntimeProbeTimeout = 30 * time.Second
)

type goldenPathPackageEvidence struct {
	Package string `json:"package"`
	Version string `json:"version"`
}

type goldenPathBrowserEvidence struct {
	Engine            string `json:"engine"`
	PlaywrightPackage string `json:"playwright_package"`
	PlaywrightVersion string `json:"playwright_version"`
	Revision          string `json:"revision"`
	BrowserVersion    string `json:"browser_version"`
}

type goldenPathRuntimeEvidence struct {
	Node    string
	SDK     goldenPathPackageEvidence
	Browser goldenPathBrowserEvidence
}

type goldenPathSampleAttestation struct {
	Scenario          string `json:"scenario"`
	PackageLockSHA256 string `json:"package_lock_sha256"`
}

type goldenPathRuntimeAttestation struct {
	Node    string                    `json:"node"`
	Browser goldenPathBrowserEvidence `json:"browser"`
}

type goldenPathAttestation struct {
	Schema         string                       `json:"schema"`
	Result         string                       `json:"result"`
	SourceRevision string                       `json:"source_revision"`
	Sample         goldenPathSampleAttestation  `json:"sample"`
	SDK            goldenPathPackageEvidence    `json:"sdk"`
	Runtime        goldenPathRuntimeAttestation `json:"runtime"`
}

type installedPackageIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type playwrightBrowsersManifest struct {
	Browsers []struct {
		Name           string `json:"name"`
		Revision       string `json:"revision"`
		BrowserVersion string `json:"browserVersion"`
	} `json:"browsers"`
}

func collectGoldenPathRuntimeEvidence(sampleRoot string) (goldenPathRuntimeEvidence, error) {
	nodeVersion, err := runMetadataCommand(sampleRoot, "node", "--version")
	if err != nil {
		return goldenPathRuntimeEvidence{}, err
	}
	sdkPackage, err := os.ReadFile(filepath.Join(sampleRoot, "node_modules", goldenPathSDKPackage, "package.json"))
	if err != nil {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("read installed SDK identity: %w", err)
	}
	playwrightPackage, err := os.ReadFile(filepath.Join(sampleRoot, "node_modules", "@playwright", "test", "package.json"))
	if err != nil {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("read installed Playwright identity: %w", err)
	}
	browsersManifest, err := os.ReadFile(filepath.Join(sampleRoot, "node_modules", "playwright-core", "browsers.json"))
	if err != nil {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("read installed Playwright browser manifest: %w", err)
	}
	browserVersion, err := runMetadataCommand(
		sampleRoot,
		"node",
		"--input-type=module",
		"--eval",
		`import { chromium } from '@playwright/test'; const browser = await chromium.launch({ headless: true }); try { process.stdout.write(browser.version()); } finally { await browser.close(); }`,
	)
	if err != nil {
		return goldenPathRuntimeEvidence{}, err
	}
	return parseGoldenPathRuntimeEvidence(
		nodeVersion,
		sdkPackage,
		playwrightPackage,
		browsersManifest,
		browserVersion,
	)
}

func parseGoldenPathRuntimeEvidence(
	nodeVersion string,
	sdkPackageJSON []byte,
	playwrightPackageJSON []byte,
	browsersManifestJSON []byte,
	launchedBrowserVersion string,
) (goldenPathRuntimeEvidence, error) {
	var sdk installedPackageIdentity
	if err := json.Unmarshal(sdkPackageJSON, &sdk); err != nil {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("decode installed SDK identity: %w", err)
	}
	if sdk.Name != goldenPathSDKPackage || sdk.Version != goldenPathSDKVersion {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("unexpected installed SDK identity")
	}

	var playwright installedPackageIdentity
	if err := json.Unmarshal(playwrightPackageJSON, &playwright); err != nil {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("decode installed Playwright identity: %w", err)
	}
	if playwright.Name != goldenPathPlaywrightPackage || playwright.Version != goldenPathPlaywrightVersion {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("unexpected installed Playwright identity")
	}

	var manifest playwrightBrowsersManifest
	if err := json.Unmarshal(browsersManifestJSON, &manifest); err != nil {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("decode installed Playwright browser manifest: %w", err)
	}
	var revision, manifestBrowserVersion string
	for _, browser := range manifest.Browsers {
		if browser.Name == "chromium" {
			revision = browser.Revision
			manifestBrowserVersion = browser.BrowserVersion
			break
		}
	}
	nodeVersion = strings.TrimPrefix(strings.TrimSpace(nodeVersion), "v")
	launchedBrowserVersion = strings.TrimSpace(launchedBrowserVersion)
	if nodeVersion != goldenPathNodeVersion {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("unexpected Node.js runtime identity")
	}
	if revision != goldenPathChromiumRevision ||
		manifestBrowserVersion != goldenPathChromiumVersion ||
		launchedBrowserVersion != manifestBrowserVersion {
		return goldenPathRuntimeEvidence{}, fmt.Errorf("unexpected Chromium runtime identity")
	}

	return goldenPathRuntimeEvidence{
		Node: nodeVersion,
		SDK: goldenPathPackageEvidence{
			Package: sdk.Name,
			Version: sdk.Version,
		},
		Browser: goldenPathBrowserEvidence{
			Engine:            "chromium",
			PlaywrightPackage: playwright.Name,
			PlaywrightVersion: playwright.Version,
			Revision:          revision,
			BrowserVersion:    launchedBrowserVersion,
		},
	}, nil
}

func runMetadataCommand(directory, name string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), goldenPathRuntimeProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Stdin = nil
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("runtime metadata command %s exceeded its deadline: %w", name, ctx.Err())
		}
		return "", fmt.Errorf("runtime metadata command %s failed", name)
	}
	return strings.TrimSpace(string(output)), nil
}

func writeGoldenPathAttestation(root, outputPath string, runtimeEvidence goldenPathRuntimeEvidence) error {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		return fmt.Errorf("golden-path attestation output path is empty")
	}
	outputPath, err := boundedGoldenPathAttestationOutput(root, outputPath)
	if err != nil {
		return err
	}
	lockfilePath := filepath.Join(root, "docs-site", "examples", "javascript-web-quickstart", "package-lock.json")
	lockfile, err := os.ReadFile(lockfilePath)
	if err != nil {
		return fmt.Errorf("read golden-path package lock: %w", err)
	}
	lockHash := sha256.Sum256(lockfile)
	revision, err := gitOutput(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	if !isSourceRevision(revision) {
		return fmt.Errorf("golden-path source revision must be a 40- or 64-character lowercase hex HEAD")
	}
	receipt := goldenPathAttestation{
		Schema:         goldenPathAttestationSchema,
		Result:         "passed",
		SourceRevision: revision,
		Sample: goldenPathSampleAttestation{
			Scenario:          goldenPathScenario,
			PackageLockSHA256: hex.EncodeToString(lockHash[:]),
		},
		SDK: runtimeEvidence.SDK,
		Runtime: goldenPathRuntimeAttestation{
			Node:    runtimeEvidence.Node,
			Browser: runtimeEvidence.Browser,
		},
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode golden-path attestation: %w", err)
	}

	status, err := gitOutput(root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("refusing verified golden-path attestation from a dirty worktree")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return fmt.Errorf("create golden-path attestation directory: %w", err)
	}
	return writeFileAtomic(outputPath, data, 0o600)
}

func boundedGoldenPathAttestationOutput(root, outputPath string) (string, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve golden-path repository root: %w", err)
	}
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(rootPath, outputPath)
	}
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return "", fmt.Errorf("resolve golden-path attestation output: %w", err)
	}
	artifactRoot := filepath.Join(rootPath, "tmp", "docs-site-e2e")
	relativePath, err := filepath.Rel(artifactRoot, outputPath)
	if err != nil || relativePath == "." || relativePath == ".." ||
		strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) || filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("golden-path attestation output must be under the repository tmp/docs-site-e2e directory")
	}
	containsSymlink, err := existingDirectoryPathContainsSymlink(rootPath, filepath.Dir(outputPath))
	if err != nil {
		return "", fmt.Errorf("inspect golden-path attestation output directory: %w", err)
	}
	if containsSymlink {
		return "", fmt.Errorf("golden-path attestation output must be under the repository tmp/docs-site-e2e directory")
	}
	return outputPath, nil
}

func existingDirectoryPathContainsSymlink(rootPath, directory string) (bool, error) {
	relativePath, err := filepath.Rel(rootPath, directory)
	if err != nil {
		return false, err
	}
	currentPath := rootPath
	for _, component := range strings.Split(relativePath, string(os.PathSeparator)) {
		if component == "" || component == "." {
			continue
		}
		currentPath = filepath.Join(currentPath, component)
		info, statErr := os.Lstat(currentPath)
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		if statErr != nil {
			return false, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}

func gitOutput(root string, arguments ...string) (string, error) {
	commandArguments := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
	}, arguments...)
	command := exec.Command("git", commandArguments...)
	command.Dir = root
	command.Stdin = nil
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("inspect golden-path source identity")
	}
	return strings.TrimSpace(string(output)), nil
}

func isSourceRevision(value string) bool {
	if value != strings.ToLower(value) || (len(value) != 40 && len(value) != 64) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && (len(decoded) == 20 || len(decoded) == 32)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) (resultErr error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".golden-path-attestation-*")
	if err != nil {
		return fmt.Errorf("create temporary golden-path attestation: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	published := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, temporary.Close())
		}
		if !published {
			removeErr := os.Remove(temporaryPath)
			if !errors.Is(removeErr, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, removeErr)
			}
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return fmt.Errorf("set golden-path attestation mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write golden-path attestation: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync golden-path attestation: %w", err)
	}
	closeErr := temporary.Close()
	closed = true
	if closeErr != nil {
		return fmt.Errorf("close golden-path attestation: %w", closeErr)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish golden-path attestation atomically: %w", err)
	}
	published = true
	return nil
}
