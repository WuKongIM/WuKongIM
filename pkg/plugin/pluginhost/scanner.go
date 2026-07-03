package pluginhost

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScanPlugins discovers executable .wkp plugin binaries directly under dir.
func ScanPlugins(dir string) ([]ProcessSpec, error) {
	cleanDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return nil, fmt.Errorf("clean plugin dir %q: %w", dir, err)
	}
	entries, err := os.ReadDir(cleanDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan plugin dir %q: %w", dir, err)
	}

	resolvedDir, err := filepath.EvalSymlinks(cleanDir)
	if err != nil {
		resolvedDir = cleanDir
	}
	resolvedDir = filepath.Clean(resolvedDir)

	specs := make([]ProcessSpec, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".wkp" {
			continue
		}
		path := filepath.Join(cleanDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat plugin %q: %w", path, err)
		}
		if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("resolve plugin path %q: %w", path, err)
		}
		if err := ensurePathUnderDir(resolvedDir, resolvedPath); err != nil {
			return nil, err
		}
		no := strings.TrimSuffix(entry.Name(), ".wkp")
		if err := validatePluginNo(no); err != nil {
			return nil, err
		}
		specs = append(specs, ProcessSpec{No: no, Path: path})
	}

	sort.Slice(specs, func(i, j int) bool {
		return specs[i].Path < specs[j].Path
	})
	return specs, nil
}

func ensurePathUnderDir(dir, path string) error {
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(dir, cleanPath)
	if err != nil {
		return fmt.Errorf("validate plugin path %q: %w", path, err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("plugin path %q escapes configured dir %q", path, dir)
	}
	return nil
}
