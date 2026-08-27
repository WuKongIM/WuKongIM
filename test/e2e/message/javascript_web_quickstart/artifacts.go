//go:build e2e

package javascript_web_quickstart

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const docsQuickstartFailureScreenshotLimit = 3

type docsQuickstartBrowserArtifacts struct {
	dir string
}

func newDocsQuickstartBrowserArtifacts(root string, now time.Time) (*docsQuickstartBrowserArtifacts, error) {
	baseDir := filepath.Join(root, "tmp", "docs-site-e2e")
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, fmt.Errorf("create browser artifact root: %w", err)
	}
	dir, err := os.MkdirTemp(
		baseDir,
		fmt.Sprintf("run-%d-%d-", os.Getpid(), now.UnixNano()),
	)
	if err != nil {
		return nil, fmt.Errorf("create unique browser artifact directory: %w", err)
	}
	return &docsQuickstartBrowserArtifacts{dir: dir}, nil
}

func (a *docsQuickstartBrowserArtifacts) Dir() string {
	if a == nil {
		return ""
	}
	return a.dir
}

func (a *docsQuickstartBrowserArtifacts) Cleanup(ctx context.Context, failed bool) error {
	if a == nil || strings.TrimSpace(a.dir) == "" {
		return nil
	}
	return runBoundedCleanup(ctx, func() error {
		if !failed {
			return os.RemoveAll(a.dir)
		}
		return pruneBrowserFailureArtifacts(a.dir)
	})
}

func runBoundedCleanup(ctx context.Context, cleanup func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() {
		result <- cleanup()
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("browser artifact cleanup exceeded its deadline: %w", ctx.Err())
	}
}

func pruneBrowserFailureArtifacts(root string) error {
	var (
		pngs    []string
		cleanup error
	)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			cleanup = errors.Join(cleanup, walkErr)
			return nil
		}
		if info == nil || info.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".png") && info.Size() <= docsQuickstartScreenshotMaxBytes {
			pngs = append(pngs, path)
			return nil
		}
		cleanup = errors.Join(cleanup, os.Remove(path))
		return nil
	})
	cleanup = errors.Join(cleanup, err)
	sort.Strings(pngs)
	retainedCategories := make(map[string]struct{}, docsQuickstartFailureScreenshotLimit)
	for _, path := range pngs {
		category := browserFailureScreenshotCategory(path)
		_, duplicateCategory := retainedCategories[category]
		if category == "" || duplicateCategory || len(retainedCategories) >= docsQuickstartFailureScreenshotLimit {
			cleanup = errors.Join(cleanup, os.Remove(path))
			continue
		}
		retainedCategories[category] = struct{}{}
	}
	return cleanup
}

func browserFailureScreenshotCategory(path string) string {
	name := strings.ToLower(filepath.Base(path))
	switch {
	case strings.Contains(name, "message-flow"), strings.Contains(name, "functional"):
		return "functional"
	case strings.Contains(name, "desktop"):
		return "desktop"
	case strings.Contains(name, "mobile"):
		return "mobile"
	default:
		return ""
	}
}
