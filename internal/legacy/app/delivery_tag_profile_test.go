package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	deliveryTagProfileEnabledEnv     = "WK_DELIVERY_TAG_100K_PROFILE"
	deliveryTagProfileSubscribersEnv = "WK_DELIVERY_TAG_100K_SUBSCRIBERS"
	deliveryTagProfileArtifactDirEnv = "WK_DELIVERY_TAG_PROFILE_DIR"
	deliveryTagProfileSendTimeoutEnv = "WK_DELIVERY_TAG_100K_SEND_TIMEOUT"
)

const (
	deliveryTagProfileDefaultSubscribers = 100000
	deliveryTagProfileDefaultArtifactDir = "tmp/profiles/delivery-tag-100k"
	deliveryTagProfileDefaultSendTimeout = 5 * time.Minute
	deliveryTagProfileCPUFilename        = "delivery-tag-100k.cpu.pprof"
	deliveryTagProfileHeapBaseFilename   = "delivery-tag-100k.heap.before.pprof"
	deliveryTagProfileHeapFilename       = "delivery-tag-100k.heap.pprof"
)

// deliveryTagProfileConfig keeps the opt-in knobs and artifact paths for the 100k profile run.
type deliveryTagProfileConfig struct {
	Enabled             bool
	SubscriberCount     int
	ArtifactDir         string
	CPUProfilePath      string
	HeapBaseProfilePath string
	HeapProfilePath     string
	SendTimeout         time.Duration
}

// deliveryTagProfileRunStats records coarse memory counters around the profiled section.
type deliveryTagProfileRunStats struct {
	Elapsed          time.Duration
	AllocBefore      uint64
	AllocAfter       uint64
	TotalAllocBefore uint64
	TotalAllocAfter  uint64
	NumGCBefore      uint32
	NumGCAfter       uint32
}

func TestDeliveryTagProfileConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv(deliveryTagProfileEnabledEnv, "")
	t.Setenv(deliveryTagProfileSubscribersEnv, "")
	t.Setenv(deliveryTagProfileArtifactDirEnv, "")
	t.Setenv(deliveryTagProfileSendTimeoutEnv, "")

	cfg := loadDeliveryTagProfileConfig(t)
	require.False(t, cfg.Enabled)
	require.Equal(t, deliveryTagProfileDefaultSubscribers, cfg.SubscriberCount)
	require.Equal(t, filepath.Join("tmp", "profiles", "delivery-tag-100k"), cfg.ArtifactDir)
	require.Equal(t, filepath.Join("tmp", "profiles", "delivery-tag-100k", "delivery-tag-100k.cpu.pprof"), cfg.CPUProfilePath)
	require.Equal(t, filepath.Join("tmp", "profiles", "delivery-tag-100k", "delivery-tag-100k.heap.before.pprof"), cfg.HeapBaseProfilePath)
	require.Equal(t, filepath.Join("tmp", "profiles", "delivery-tag-100k", "delivery-tag-100k.heap.pprof"), cfg.HeapProfilePath)
	require.Equal(t, deliveryTagProfileDefaultSendTimeout, cfg.SendTimeout)

	t.Setenv(deliveryTagProfileEnabledEnv, "1")
	t.Setenv(deliveryTagProfileSubscribersEnv, "12345")
	t.Setenv(deliveryTagProfileArtifactDirEnv, filepath.Join("tmp", "custom-delivery-tag-profile"))
	t.Setenv(deliveryTagProfileSendTimeoutEnv, "45s")

	cfg = loadDeliveryTagProfileConfig(t)
	require.True(t, cfg.Enabled)
	require.Equal(t, 12345, cfg.SubscriberCount)
	require.Equal(t, filepath.Join("tmp", "custom-delivery-tag-profile"), cfg.ArtifactDir)
	require.Equal(t, filepath.Join("tmp", "custom-delivery-tag-profile", "delivery-tag-100k.cpu.pprof"), cfg.CPUProfilePath)
	require.Equal(t, filepath.Join("tmp", "custom-delivery-tag-profile", "delivery-tag-100k.heap.before.pprof"), cfg.HeapBaseProfilePath)
	require.Equal(t, filepath.Join("tmp", "custom-delivery-tag-profile", "delivery-tag-100k.heap.pprof"), cfg.HeapProfilePath)
	require.Equal(t, 45*time.Second, cfg.SendTimeout)
}

func loadDeliveryTagProfileConfig(t *testing.T) deliveryTagProfileConfig {
	t.Helper()

	enabled, _ := parseDeliveryTagProfileBool(os.LookupEnv(deliveryTagProfileEnabledEnv))
	subscriberCount, err := deliveryTagProfileEnvInt(deliveryTagProfileSubscribersEnv, deliveryTagProfileDefaultSubscribers)
	require.NoError(t, err)
	require.GreaterOrEqual(t, subscriberCount, 5, "%s must be >= 5", deliveryTagProfileSubscribersEnv)
	artifactDir, err := deliveryTagProfileEnvString(deliveryTagProfileArtifactDirEnv, deliveryTagProfileDefaultArtifactDir)
	require.NoError(t, err)
	sendTimeout, err := deliveryTagProfileEnvDuration(deliveryTagProfileSendTimeoutEnv, deliveryTagProfileDefaultSendTimeout)
	require.NoError(t, err)
	if strings.TrimSpace(artifactDir) == "" {
		artifactDir = deliveryTagProfileDefaultArtifactDir
	}

	return deliveryTagProfileConfig{
		Enabled:             enabled,
		SubscriberCount:     subscriberCount,
		ArtifactDir:         artifactDir,
		CPUProfilePath:      filepath.Join(artifactDir, deliveryTagProfileCPUFilename),
		HeapBaseProfilePath: filepath.Join(artifactDir, deliveryTagProfileHeapBaseFilename),
		HeapProfilePath:     filepath.Join(artifactDir, deliveryTagProfileHeapFilename),
		SendTimeout:         sendTimeout,
	}
}

func parseDeliveryTagProfileBool(value string, ok bool) (bool, bool) {
	if !ok || strings.TrimSpace(value) == "" {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, true
	}
}

func deliveryTagProfileEnvInt(name string, fallback int) (int, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be > 0, got %d", name, parsed)
	}
	return parsed, nil
}

func deliveryTagProfileEnvDuration(name string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be > 0, got %s", name, parsed)
	}
	return parsed, nil
}

func deliveryTagProfileEnvString(name, fallback string) (string, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return strings.TrimSpace(value), nil
}

func runDeliveryTagProfile(t *testing.T, cfg deliveryTagProfileConfig, fn func()) (stats deliveryTagProfileRunStats) {
	t.Helper()

	require.NoError(t, os.MkdirAll(cfg.ArtifactDir, 0o755))
	runtime.GC()
	writeDeliveryTagHeapProfile(t, cfg.HeapBaseProfilePath)

	cpuFile, err := os.Create(cfg.CPUProfilePath)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, cpuFile.Close())
	}()

	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	stats.AllocBefore = before.Alloc
	stats.TotalAllocBefore = before.TotalAlloc
	stats.NumGCBefore = before.NumGC

	require.NoError(t, pprof.StartCPUProfile(cpuFile))
	started := time.Now()
	defer func() {
		pprof.StopCPUProfile()
		runtime.GC()

		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		stats.Elapsed = time.Since(started)
		stats.AllocAfter = after.Alloc
		stats.TotalAllocAfter = after.TotalAlloc
		stats.NumGCAfter = after.NumGC

		writeDeliveryTagHeapProfile(t, cfg.HeapProfilePath)

		t.Logf(
			"delivery tag profile artifacts: cpu=%s heap_base=%s heap=%s elapsed=%s alloc_before=%d alloc_after=%d total_alloc_before=%d total_alloc_after=%d num_gc_before=%d num_gc_after=%d",
			cfg.CPUProfilePath,
			cfg.HeapBaseProfilePath,
			cfg.HeapProfilePath,
			stats.Elapsed,
			stats.AllocBefore,
			stats.AllocAfter,
			stats.TotalAllocBefore,
			stats.TotalAllocAfter,
			stats.NumGCBefore,
			stats.NumGCAfter,
		)
	}()

	fn()
	return stats
}

func writeDeliveryTagHeapProfile(t *testing.T, path string) {
	t.Helper()

	heapFile, err := os.Create(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, heapFile.Close())
	}()
	require.NoError(t, pprof.WriteHeapProfile(heapFile))
}
