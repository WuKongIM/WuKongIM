// Package dataformat identifies node directory formats without opening databases.
// It does not infer the format or creator of an existing unregistered directory.
package dataformat

import (
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
	FileName      = "DATA-FORMAT.json"
	CurrentFormat = "wukongim-v3"
	// CurrentVersion changes only when the directory format requires a migration.
	CurrentVersion = 2
	maxBytes       = 16 << 10
)

var ErrUnsupported = errors.New("unsupported data directory format; use a compatible server or migrate the data")

// Build records the creator, not the last process to open the directory.
type Build struct {
	Program     string `json:"program"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildSource string `json:"build_source"`
}

// Metadata is immutable creation provenance plus an independent format contract.
// Ordinary software upgrades must not rewrite these fields.
type Metadata struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	CreatedBy     Build     `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

// Report distinguishes an unregistered directory from an incompatible format.
type Report struct {
	Status   string    `json:"status"`
	Metadata *Metadata `json:"metadata,omitempty"`
}

// Inspect reads at most maxBytes and never opens an engine, locks, or writes files.
// Missing directories are errors; existing directories without a marker are unknown.
func Inspect(dir string) (Report, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return Report{}, err
	}
	if !info.IsDir() {
		return Report{}, errors.New("data directory is not a directory")
	}
	path := filepath.Join(dir, FileName)
	info, err = os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Report{Status: "unregistered"}, nil
	}
	if err != nil {
		return Report{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return Report{}, errors.New("invalid data format marker: expected a bounded regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return Report{}, err
	}
	if !os.SameFile(info, opened) {
		return Report{}, errors.New("data format marker changed while opening")
	}
	d := json.NewDecoder(io.LimitReader(f, maxBytes+1))
	d.DisallowUnknownFields()
	var m Metadata
	if err := d.Decode(&m); err != nil {
		return Report{}, fmt.Errorf("invalid data format marker: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Report{}, errors.New("invalid data format marker: trailing data")
	}
	if m.Format == "" || m.FormatVersion <= 0 || m.CreatedAt.IsZero() || m.CreatedBy.Program == "" || m.CreatedBy.Version == "" || m.CreatedBy.Commit == "" || m.CreatedBy.BuildSource == "" {
		return Report{}, errors.New("invalid data format marker: missing creation or format fields")
	}
	status := "registered"
	if m.Format != CurrentFormat || m.FormatVersion != CurrentVersion {
		status = "unsupported"
	}
	return Report{Status: status, Metadata: &m}, nil
}

// Check rejects unsupported/corrupt markers before writable stores are opened.
// Unregistered live data requires an explicit compatible import. An archive-only
// backup-repository mount does not register or adopt any live database state.
func Check(dir string) error {
	r, err := Inspect(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Status == "unregistered" {
		empty, err := emptyDataRoot(dir)
		if err != nil {
			return err
		}
		if !empty {
			return fmt.Errorf("%w: unregistered nonempty directory", ErrUnsupported)
		}
	}
	if r.Status == "unsupported" {
		return fmt.Errorf("%w: %s version %d", ErrUnsupported, r.Metadata.Format, r.Metadata.FormatVersion)
	}
	return nil
}

// EnsureFresh registers fresh roots whose other durable paths are empty.
// Existing live data, including external Controller state, is rejected without
// a supported marker. A pre-mounted backup repository contains archives only.
func EnsureFresh(dir string, build Build, durablePaths ...string) error {
	if err := Check(dir); err != nil {
		return err
	}
	for i, p := range append([]string{dir}, durablePaths...) {
		if p == "" {
			continue
		}
		var empty bool
		var err error
		if i == 0 {
			empty, err = emptyDataRoot(p)
		} else {
			empty, err = emptyDirectory(p)
		}
		if err != nil {
			return err
		}
		if !empty {
			if r, err := Inspect(dir); err == nil && r.Status == "registered" {
				return nil
			}
			return fmt.Errorf("%w: unregistered durable state", ErrUnsupported)
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return InitializeOwned(dir, build)
}

// emptyDataRoot permits an archive-only mount prepared before first startup.
// Every other entry denotes an unregistered live generation and stays rejected.
// Read at most two names; repository contents are never read or adopted.
func emptyDataRoot(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	entries, err := f.ReadDir(2)
	if err != nil && err != io.EOF {
		return false, err
	}
	if len(entries) == 0 {
		return true, nil
	}
	if len(entries) != 1 || entries[0].Name() != "backup-repository" {
		return false, nil
	}
	info, err := os.Stat(filepath.Join(path, "backup-repository"))
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func emptyDirectory(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

// InitializeOwned atomically publishes creation metadata for an exclusively owned
// new generation. Importers may call it in their locked staging directory before
// sealing files. It must never be used to adopt arbitrary historical directories.
// Existing supported markers are preserved byte-for-byte on retries and upgrades.
func InitializeOwned(dir string, build Build) (err error) {
	r, err := Inspect(dir)
	if err != nil {
		return err
	}
	if r.Status != "unregistered" {
		return Check(dir)
	}
	normalize := func(s string) string {
		if strings.TrimSpace(s) == "" {
			return "unknown"
		}
		return s
	}
	build = Build{normalize(build.Program), normalize(build.Version), normalize(build.Commit), normalize(build.BuildSource)}
	m := Metadata{Format: CurrentFormat, FormatVersion: CurrentVersion, CreatedBy: build, CreatedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxBytes {
		return errors.New("data format marker exceeds size limit")
	}
	f, err := os.CreateTemp(dir, ".data-format-")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Linking publishes a complete file without replacing another creator's marker.
	if err = os.Link(f.Name(), filepath.Join(dir, FileName)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Check(dir)
		}
		return err
	}
	if err = os.Remove(f.Name()); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
