package migration

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"reflect"

	artifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

var sourceArchivePrefixes = [...]string{"source/", "catalog/", "selected/", "plugin-artifacts/"}

// PreparedArchiveSeal binds a successful report and every raw exported row.
// It accelerates publication only; import and verify still independently
// rebuild all semantic checks from the original archive records.
type PreparedArchiveSeal struct {
	Version         int    `json:"version"`
	ReportSHA256    string `json:"report_sha256"`
	WorkspaceSHA256 string `json:"workspace_sha256"`
	Rows            uint64 `json:"rows"`
}

// archiveRowDigest streams ordered export bytes with constant memory.
type archiveRowDigest struct {
	h    hash.Hash
	rows uint64
}

func newArchiveRowDigest() *archiveRowDigest {
	h := sha256.New()
	h.Write([]byte("wkmigrate-export-seal-v1\n"))
	return &archiveRowDigest{h: h}
}

// add uses length framing so key/value boundaries cannot collide.
func (d *archiveRowDigest) add(row transfer.SpoolRow) {
	var size [8]byte
	for _, data := range [][]byte{row.Key, row.Value} {
		binary.BigEndian.PutUint64(size[:], uint64(len(data)))
		d.h.Write(size[:])
		d.h.Write(data)
	}
	d.rows++
}

func preparedReportSHA(p Preflight) (string, error) {
	p.ArchiveSeal = nil
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return diagnosticSHA(data), nil
}

func sealPreparedArchive(ctx context.Context, w Workspace, p *Preflight) error {
	w = quarantineRawWorkspace(w)
	d := newArchiveRowDigest()
	for _, prefix := range sourceArchivePrefixes {
		if err := w.Walk(ctx, []byte(prefix), func(row transfer.SpoolRow) error { d.add(row); return nil }); err != nil {
			return err
		}
	}
	report, err := preparedReportSHA(*p)
	if err != nil {
		return err
	}
	p.ArchiveSeal = &PreparedArchiveSeal{Version: 1, ReportSHA256: report, WorkspaceSHA256: hex.EncodeToString(d.h.Sum(nil)), Rows: d.rows}
	return nil
}

// ExportPreparedArchive rechecks the stopped source and exact artifact bytes,
// then verifies the prepared export seal during publication. It never repeats
// catalog joins, replica reduction or native conversion from a successful run.
func ExportPreparedArchive(ctx context.Context, plan Plan, w Workspace, source Source, store artifact.ArchiveStore, progress func(uint64, string)) (SourceArchiveManifest, error) {
	var zero SourceArchiveManifest
	if ctx == nil || w == nil || source == nil || store == nil {
		return zero, errors.New("prepared export requires source, workspace and archive")
	}
	raw, found, err := w.Get(ctx, []byte("workflow/PREPARED"))
	if err != nil {
		return zero, err
	}
	if !found || len(raw) > 32<<20 {
		return zero, errors.New("run prepare successfully before export")
	}
	var p Preflight
	if err := json.Unmarshal(raw, &p); err != nil {
		return zero, err
	}
	if p.Status != "prepared" || p.CutoverReady || p.PlanDigest != plan.Digest() || p.SourceCommit != plan.SourceCommit || p.Selection.Digest == "" || p.Conversion.SelectionDigest != p.Selection.Digest {
		return zero, errors.New("prepared export report does not match this plan")
	}
	if p.ArchiveSeal == nil || p.ArchiveSeal.Version != 1 || p.ArchiveSeal.Rows == 0 {
		return zero, errors.New("prepare checkpoint has no export seal; use a fresh workspace with matching migration tools")
	}
	sha, err := preparedReportSHA(p)
	if err != nil {
		return zero, err
	}
	if sha != p.ArchiveSeal.ReportSHA256 {
		return zero, errors.New("prepared export report checksum mismatch")
	}
	if progress != nil {
		progress(0, "export source freshness check started")
	}
	capture, err := CaptureSources(ctx, plan.Sources, source, w, progress)
	if err != nil {
		return zero, err
	}
	if !reflect.DeepEqual(capture, p.Capture) {
		return zero, errors.New("prepared export source capture changed")
	}
	artifacts, _ := source.(PluginArtifactSource)
	if err := CapturePluginArtifacts(ctx, plan, w, artifacts); err != nil {
		return zero, err
	}
	if progress != nil {
		progress(0, "export source freshness checked; publishing sealed preparation")
	}
	return exportSourceArchive(ctx, SourceArchiveOptions{PlanDigest: plan.Digest(), SourceCommit: plan.SourceCommit}, p.Capture, p.Catalog, p.Selection, w, store, p.ArchiveSeal)
}
