//go:build e2e

package javascript_web_quickstart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBrowserArtifactsUseUniqueRunDirectoriesAndCleanSuccessfulRun(t *testing.T) {
	root := t.TempDir()
	now := time.Unix(0, 1_788_000_000_123_456_789)

	first, err := newDocsQuickstartBrowserArtifacts(root, now)
	require.NoError(t, err)
	second, err := newDocsQuickstartBrowserArtifacts(root, now)
	require.NoError(t, err)
	require.NotEqual(t, first.Dir(), second.Dir())
	require.Contains(t, filepath.Base(first.Dir()), "1788000000123456789")
	require.Contains(t, filepath.Base(second.Dir()), "1788000000123456789")
	require.NoError(t, os.WriteFile(filepath.Join(first.Dir(), "success.png"), []byte("png"), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, first.Cleanup(ctx, false))
	_, err = os.Stat(first.Dir())
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, second.Cleanup(ctx, false))
}

func TestFailedBrowserArtifactsKeepOnlyThreeBoundedPNGsInStableOrder(t *testing.T) {
	artifacts, err := newDocsQuickstartBrowserArtifacts(
		t.TempDir(),
		time.Unix(0, 1_788_000_000_987_654_321),
	)
	require.NoError(t, err)

	for _, name := range []string{
		"d-extra.png",
		"b-desktop.png",
		"a-functional.png",
		"aa-functional-duplicate.png",
		"c-mobile.png",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(artifacts.Dir(), name), []byte("png"), 0o600))
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(artifacts.Dir(), "e-oversized.png"),
		make([]byte, docsQuickstartScreenshotMaxBytes+1),
		0o600,
	))
	require.NoError(t, os.WriteFile(filepath.Join(artifacts.Dir(), "error-context.md"), []byte("details"), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, artifacts.Cleanup(ctx, true))

	var retained []string
	require.NoError(t, filepath.Walk(artifacts.Dir(), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil || info.IsDir() {
			return walkErr
		}
		retained = append(retained, strings.TrimPrefix(path, artifacts.Dir()+string(os.PathSeparator)))
		return nil
	}))
	sort.Strings(retained)
	require.Equal(t, []string{"a-functional.png", "b-desktop.png", "c-mobile.png"}, retained)
	for _, name := range retained {
		info, statErr := os.Stat(filepath.Join(artifacts.Dir(), name))
		require.NoError(t, statErr)
		require.LessOrEqual(t, info.Size(), int64(docsQuickstartScreenshotMaxBytes))
	}
}

func TestBoundedCleanupStopsWaitingAtItsDeadline(t *testing.T) {
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := runBoundedCleanup(ctx, func() error {
		<-release
		return nil
	})
	close(release)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
}
