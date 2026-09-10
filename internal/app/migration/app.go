// Package migration composes the standalone offline migration application.
package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/access/migratecli"
	archivefs "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv2"
	"github.com/WuKongIM/WuKongIM/internal/infra/migrationv3"
	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/dataformat"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// Run wires original source decoding, immutable scratch storage and archives.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return RunWithBuild(ctx, args, stdout, stderr, dataformat.Build{})
}

// RunWithBuild binds creator provenance only when installing a new generation.
func RunWithBuild(ctx context.Context, args []string, stdout, stderr io.Writer, build dataformat.Build) int {
	return migratecli.Run(ctx, args, stdout, stderr, func(ctx context.Context, cmd migratecli.Command) (result any, err error) {
		f, err := os.Open(cmd.PlanPath)
		if err != nil {
			return nil, err
		}
		plan, readErr := usecase.ReadPlan(f, migrationv2.SourceCommit)
		err = errors.Join(readErr, f.Close())
		if err != nil {
			return nil, err
		}
		if err := migrationv3.ValidatePlan(ctx, plan.Target); err != nil {
			return nil, err
		}
		if err := validatePaths(plan, cmd); err != nil {
			return nil, err
		}
		identity := plan.Digest()
		if cmd.Verb == "dedupe-plan" {
			identity += ":dedupe-v5"
		}
		if cmd.Verb == "diagnose" {
			identity += ":diagnose-v2"
		}
		if cmd.Verb == "authority" {
			identity += ":authority-v3"
		}
		w, err := transfer.OpenSpool(cmd.WorkspacePath, identity, 128<<20)
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, w.Close()) }()
		if cmd.Verb == "export" {
			if _, found, err := w.Get(ctx, []byte("workflow/PREPARED")); err != nil {
				return nil, err
			} else if !found {
				return nil, errors.New("run prepare successfully before export")
			}
		}
		r := migrationv2.Reader{}
		if cmd.Verb == "dedupe-plan" {
			return dedupePlan(ctx, plan, cmd.WorkspacePath, w, r, stderr)
		}
		if cmd.Verb == "authority" {
			return authority(ctx, plan, cmd.WorkspacePath, w, r, stderr)
		}
		if cmd.Verb == "diagnose" {
			return diagnose(ctx, plan, cmd.WorkspacePath, w, r, stderr)
		}
		if cmd.Verb == "import" || cmd.Verb == "verify" || cmd.Verb == "export-map" {
			archive, err := archivefs.NewFileArchiveStore(cmd.ArchivePath)
			if err != nil {
				return nil, err
			}
			prepared, err := usecase.PrepareArchive(ctx, plan, w, r, archive)
			if err != nil {
				return nil, err
			}
			if cmd.Verb == "export-map" {
				mapping, err := writeSequenceMap(ctx, cmd.WorkspacePath, w, prepared)
				if err != nil {
					return nil, err
				}
				if mapping == nil {
					return nil, errors.New("archive has no message transformation policy")
				}
				return mapping, nil
			}
			if cmd.Verb == "verify" {
				artifacts, err := usecase.VerifyPluginArtifacts(ctx, plan, w, migrationv3.Inspector{})
				if err != nil {
					return nil, err
				}
				settings, err := usecase.VerifyPluginSettings(ctx, plan, prepared.Capture, w, r, migrationv3.Inspector{})
				if err != nil {
					return nil, err
				}
				verified, err := usecase.VerifyTargets(ctx, plan.Target, prepared.Selection, w, r, migrationv3.Inspector{})
				if err != nil {
					return nil, err
				}
				verified.PluginSettings = &settings
				verified.PluginArtifacts = &artifacts
				return verified, nil
			}
			if err := migrationv3.Install(ctx, plan.Target, prepared.Conversion, w, migrationv3.InstallOptions{CreatedBy: build, PluginSettings: prepared.PluginSettings, PluginArtifacts: prepared.PluginArtifacts}); err != nil {
				return nil, err
			}
			return struct {
				Status       string `json:"status"`
				CutoverReady bool   `json:"cutover_ready"`
				PlanDigest   string `json:"plan_digest"`
				Nodes        int    `json:"nodes"`
			}{"imported", false, plan.Digest(), len(plan.Target.Nodes)}, nil
		}
		if cmd.Verb == "export" {
			archive, err := archivefs.NewFileArchiveStore(cmd.ArchivePath)
			if err != nil {
				return nil, err
			}
			return usecase.ExportPreparedArchive(ctx, plan, w, r, archive, func(node uint64, stage string) { fmt.Fprintf(stderr, "source node %d: %s\n", node, stage) })
		}
		prepared, err := usecase.Prepare(ctx, plan, w, r, r, func(node uint64, stage string) { fmt.Fprintf(stderr, "source node %d: %s\n", node, stage) })
		if err != nil {
			if cmd.Verb == "prepare" {
				prepared.Status = "blocked"
				return prepared, err
			}
			return nil, err
		}
		if cmd.Verb == "prepare" {
			mapping, err := writeSequenceMap(ctx, cmd.WorkspacePath, w, prepared)
			if err != nil {
				return nil, err
			}
			return struct {
				usecase.Preflight
				Mapping *sequenceMapFile `json:"sequence_mapping,omitempty"`
			}{prepared, mapping}, nil
		}
		return nil, errors.New("unsupported migration command")
	})
}

func validatePaths(plan usecase.Plan, cmd migratecli.Command) error {
	var sources, outputs []string
	for _, node := range plan.Sources {
		p, err := resolvedPath(node.DataDir)
		if err != nil {
			return err
		}
		for _, source := range sources {
			if overlaps(p, source) {
				return errors.New("original source directories overlap")
			}
		}
		sources = append(sources, p)
	}
	for _, artifact := range plan.PluginArtifacts {
		p, err := resolvedPath(artifact.Path)
		if err != nil {
			return err
		}
		sources = append(sources, p)
	}
	paths := []string{cmd.WorkspacePath}
	if cmd.ArchivePath != "" {
		paths = append(paths, cmd.ArchivePath)
	}
	for _, node := range plan.Target.Nodes {
		paths = append(paths, node.DataDir)
	}
	for _, path := range paths {
		p, err := resolvedPath(path)
		if err != nil {
			return err
		}
		for _, source := range sources {
			if overlaps(p, source) {
				return errors.New("migration output overlaps an original source directory")
			}
		}
		for _, output := range outputs {
			if overlaps(p, output) {
				return errors.New("migration output directories overlap")
			}
		}
		outputs = append(outputs, p)
	}
	return nil
}

func resolvedPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("migration directories must be absolute")
	}
	path = filepath.Clean(path)
	cursor := path
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(cursor)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", err
		}
		suffix = append(suffix, filepath.Base(cursor))
		cursor = parent
	}
}
func overlaps(a, b string) bool {
	within := func(parent, child string) bool {
		relative, err := filepath.Rel(parent, child)
		return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}
