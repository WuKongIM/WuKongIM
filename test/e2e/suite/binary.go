//go:build e2e

package suite

import (
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
	e2eBinaryCacheDirNameGlob  = "wukongim-e2e-bin-*"
	e2eBinaryBuildPackage      = "./cmd/wukongim"
	e2eBinaryBuildCommandLabel = "go build -tags=e2e ./cmd/wukongim"
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
		c.err = build(c.path)
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
		defaultBinaryRoot.path, defaultBinaryRoot.err = os.MkdirTemp("", e2eBinaryCacheDirNameGlob)
	})
	return defaultBinaryRoot.path, defaultBinaryRoot.err
}

func buildBinary(dst string) error {
	cmd := exec.Command("go", "build", "-tags=e2e", "-o", dst, e2eBinaryBuildPackage)
	cmd.Dir = repoRoot()
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
