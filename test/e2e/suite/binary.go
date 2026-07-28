//go:build e2e

package suite

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	e2eBinaryOverrideEnv       = "WK_E2E_BINARY"
	e2eBinaryCacheFileName     = "wukongim-e2e"
	e2eBinaryCacheNamespace    = "wukongim"
	e2eBinaryCacheDirectory    = "e2e-binary"
	e2eBinaryBuildPackage      = "./cmd/wukongim"
	e2eBinaryBuildCommandLabel = "go build -tags=e2e with commit-bound backup qualification ./cmd/wukongim"
)

// BinaryCache builds and caches the e2e wukongim binary once per test process.
type BinaryCache struct {
	once  sync.Once
	path  string
	err   error
	build func(dst string) error
}

var defaultBinaryCache BinaryCache

var defaultBinaryRoot struct {
	once sync.Once
	path string
	err  error
}

// Path returns the cached binary path, building it on first use.
func (c *BinaryCache) Path(tempRoot string) (string, error) {
	c.once.Do(func() {
		c.err = os.MkdirAll(tempRoot, 0o755)
		if c.err != nil {
			return
		}
		c.path = filepath.Join(tempRoot, e2eBinaryCacheFileName)
		build := c.build
		if build == nil {
			build = buildBinary
		}
		c.err = buildBinaryAtomically(c.path, build)
	})
	return c.path, c.err
}

func resolveBinaryPath() (string, error) {
	if path, ok, err := resolveBinaryOverride(e2eBinaryOverrideEnv); ok || err != nil {
		return path, err
	}

	root, err := defaultBinaryCacheRoot()
	if err != nil {
		return "", err
	}
	return defaultBinaryCache.Path(root)
}

func resolveBinaryOverride(envName string) (string, bool, error) {
	override := strings.TrimSpace(os.Getenv(envName))
	if override == "" {
		return "", false, nil
	}
	if _, err := os.Stat(override); err != nil {
		return "", true, fmt.Errorf("%s=%q: %w", envName, override, err)
	}
	return override, true, nil
}

func defaultBinaryCacheRoot() (string, error) {
	defaultBinaryRoot.once.Do(func() {
		userCacheRoot, err := os.UserCacheDir()
		if err != nil {
			defaultBinaryRoot.err = fmt.Errorf("resolve user cache directory: %w", err)
			return
		}

		repositoryKey := sha256.Sum256([]byte(repoRoot()))
		defaultBinaryRoot.path = filepath.Join(
			userCacheRoot,
			e2eBinaryCacheNamespace,
			e2eBinaryCacheDirectory,
			fmt.Sprintf("%s-%s-%x", runtime.GOOS, runtime.GOARCH, repositoryKey[:8]),
		)
		defaultBinaryRoot.err = os.MkdirAll(defaultBinaryRoot.path, 0o755)
	})
	return defaultBinaryRoot.path, defaultBinaryRoot.err
}

// buildBinaryAtomically keeps the shared cache path executable while concurrent
// test processes build and publish their own complete binaries.
func buildBinaryAtomically(dst string, build func(string) error) error {
	staged, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+"-build-*")
	if err != nil {
		return fmt.Errorf("create staged e2e binary: %w", err)
	}
	stagedPath := staged.Name()
	if err := staged.Close(); err != nil {
		_ = os.Remove(stagedPath)
		return fmt.Errorf("close staged e2e binary: %w", err)
	}
	defer os.Remove(stagedPath)

	if err := build(stagedPath); err != nil {
		return err
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		return fmt.Errorf("make staged e2e binary executable: %w", err)
	}
	if err := os.Rename(stagedPath, dst); err != nil {
		return fmt.Errorf("publish staged e2e binary: %w", err)
	}
	return nil
}

func buildBinary(dst string) error {
	root := repoRoot()
	revisionCommand := exec.Command("git", "rev-parse", "HEAD")
	revisionCommand.Dir = root
	revisionOutput, err := revisionCommand.Output()
	if err != nil {
		return fmt.Errorf("resolve e2e source revision: %w", err)
	}
	revision := strings.TrimSpace(string(revisionOutput))
	cmd := exec.Command(
		"go", "build", "-tags=e2e",
		"-ldflags=-X github.com/WuKongIM/WuKongIM/internal/app.backupQualifiedRevision="+revision,
		"-o", dst, e2eBinaryBuildPackage,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", e2eBinaryBuildCommandLabel, err, output)
	}
	return nil
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
