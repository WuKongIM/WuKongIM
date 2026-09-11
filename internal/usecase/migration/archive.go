package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	artifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

const (
	sourceArchiveFormat     = "wkmigrate-source"
	sourceArchiveVersion    = 1
	maxArchiveRecordBytes   = 256 << 20
	maxArchiveManifestBytes = 32 << 20
	maxArchiveChunks        = 1 << 20
)

// SourceArchiveOptions binds portable output to one migration plan. ChunkBytes
// bounds ordinary chunks; one oversized source row remains an indivisible chunk
// and is limited to 256 MiB after JSON encoding.
type SourceArchiveOptions struct {
	PlanDigest   string `json:"plan_digest"`
	SourceCommit string `json:"source_commit"`
	ChunkBytes   int    `json:"chunk_bytes"`
}

type SourceArchiveChunk struct {
	Path   string `json:"path"`
	Prefix string `json:"prefix"`
	Rows   uint64 `json:"rows"`
	Bytes  uint64 `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// SourceArchiveManifest binds raw rows, index bytes, source provenance, identity
// joins and selected references. COMPLETE certifies archive integrity only;
// it never certifies a target import or authorizes business cutover.
type SourceArchiveManifest struct {
	Format    string               `json:"format"`
	Version   int                  `json:"version"`
	Options   SourceArchiveOptions `json:"options"`
	Capture   SourceCapture        `json:"capture"`
	Catalog   SourceCatalog        `json:"catalog"`
	Selection SourceSelection      `json:"selection"`
	Chunks    []SourceArchiveChunk `json:"chunks"`
}

type sourceArchiveIdentity struct {
	Format    string               `json:"format"`
	Version   int                  `json:"version"`
	Options   SourceArchiveOptions `json:"options"`
	Capture   string               `json:"capture"`
	Catalog   string               `json:"catalog"`
	Selection string               `json:"selection"`
}

// ExportSourceArchive writes deterministic bounded chunks and publishes its
// manifest last. Exact objects can be resumed; no existing object is replaced.
// The coordinator must have rebound the workspace to unchanged stopped sources.
func ExportSourceArchive(ctx context.Context, options SourceArchiveOptions, capture SourceCapture, catalog SourceCatalog, selection SourceSelection, workspace Workspace, store artifact.ArchiveStore) (manifest SourceArchiveManifest, err error) {
	return exportSourceArchive(ctx, options, capture, catalog, selection, workspace, store, nil)
}

func exportSourceArchive(ctx context.Context, options SourceArchiveOptions, capture SourceCapture, catalog SourceCatalog, selection SourceSelection, workspace Workspace, store artifact.ArchiveStore, seal *PreparedArchiveSeal) (manifest SourceArchiveManifest, err error) {
	if options.ChunkBytes == 0 {
		options.ChunkBytes = 8 << 20
	}
	if ctx == nil || workspace == nil || store == nil || options.PlanDigest == "" || len(options.PlanDigest) > 256 || len(options.SourceCommit) != 40 || options.ChunkBytes < 1024 || options.ChunkBytes > 64<<20 || capture.Digest == "" || catalog.Digest == "" || selection.Digest == "" {
		return manifest, errors.New("source archive requires a completed source selection and immutable plan")
	}
	manifest = SourceArchiveManifest{Format: sourceArchiveFormat, Version: sourceArchiveVersion, Options: options, Capture: capture, Catalog: catalog, Selection: selection}
	identity, err := json.Marshal(sourceArchiveIdentity{Format: manifest.Format, Version: manifest.Version, Options: options, Capture: capture.Digest, Catalog: catalog.Digest, Selection: selection.Digest})
	if err != nil {
		return manifest, err
	}
	if err := putExactArchiveObject(ctx, store, "IDENTITY", identity); err != nil {
		return manifest, err
	}
	var buffer bytes.Buffer
	var rows uint64
	digest := newArchiveRowDigest()
	for _, prefix := range sourceArchivePrefixes {
		flush := func() error {
			if rows == 0 {
				return nil
			}
			if len(manifest.Chunks) >= maxArchiveChunks {
				return errors.New("migration archive exceeds chunk limit")
			}
			sum := sha256.Sum256(buffer.Bytes())
			chunk := SourceArchiveChunk{Path: fmt.Sprintf("chunks/%08d.jsonl", len(manifest.Chunks)), Prefix: prefix, Rows: rows, Bytes: uint64(buffer.Len()), SHA256: hex.EncodeToString(sum[:])}
			if err := putExactArchiveObject(ctx, store, chunk.Path, buffer.Bytes()); err != nil {
				return err
			}
			manifest.Chunks = append(manifest.Chunks, chunk)
			// Drop an exceptional large allocation before ordinary chunks resume.
			if buffer.Cap() > options.ChunkBytes*2 {
				buffer = bytes.Buffer{}
			} else {
				buffer.Reset()
			}
			rows = 0
			return nil
		}
		err := workspace.Walk(ctx, []byte(prefix), func(row transfer.SpoolRow) error {
			if seal != nil {
				digest.add(row)
			}
			data, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if len(data) >= maxArchiveRecordBytes {
				return errors.New("migration archive record exceeds 256 MiB")
			}
			if rows > 0 && (len(data)+1 > options.ChunkBytes-buffer.Len() || rows >= 1000000) {
				if err := flush(); err != nil {
					return err
				}
			}
			_, _ = buffer.Write(data)
			_ = buffer.WriteByte('\n')
			rows++
			if buffer.Len() >= options.ChunkBytes {
				return flush()
			}
			return nil
		})
		if err != nil {
			return manifest, err
		}
		if err := flush(); err != nil {
			return manifest, err
		}
	}
	if seal != nil && (digest.rows != seal.Rows || hex.EncodeToString(digest.h.Sum(nil)) != seal.WorkspaceSHA256) {
		return manifest, errors.New("prepared export workspace checksum mismatch; archive remains incomplete")
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return manifest, err
	}
	if len(data) > maxArchiveManifestBytes {
		return manifest, errors.New("migration archive manifest exceeds 32 MiB")
	}
	if err := putExactArchiveObject(ctx, store, "manifest.json", data); err != nil {
		return manifest, err
	}
	sum := sha256.Sum256(data)
	if err := putExactArchiveObject(ctx, store, "COMPLETE", []byte(hex.EncodeToString(sum[:]))); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// ReadSourceArchive verifies publication and every complete chunk before
// visiting its records. Callers must stage into an isolated workspace and
// require successful completion before consuming any selected business data.
func ReadSourceArchive(ctx context.Context, store artifact.ArchiveStore, visit func(transfer.SpoolRow) error) (manifest SourceArchiveManifest, err error) {
	if ctx == nil || store == nil || visit == nil {
		return manifest, errors.New("source archive read requires store and visitor")
	}
	complete, err := readArchiveObject(ctx, store, "COMPLETE", 64)
	if err != nil {
		return manifest, err
	}
	data, err := readArchiveObject(ctx, store, "manifest.json", maxArchiveManifestBytes)
	if err != nil {
		return manifest, err
	}
	sum := sha256.Sum256(data)
	if string(complete) != hex.EncodeToString(sum[:]) {
		return manifest, errors.New("migration archive manifest checksum mismatch")
	}
	if err := decodeArchiveJSON(data, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Format != sourceArchiveFormat || manifest.Version != sourceArchiveVersion || len(manifest.Chunks) == 0 || len(manifest.Chunks) > maxArchiveChunks || manifest.Options.PlanDigest == "" || manifest.Capture.Digest == "" || manifest.Catalog.Digest == "" || manifest.Selection.Digest == "" {
		return manifest, errors.New("invalid migration source manifest")
	}
	identity, err := readArchiveObject(ctx, store, "IDENTITY", 4096)
	if err != nil {
		return manifest, err
	}
	wantIdentity, err := json.Marshal(sourceArchiveIdentity{Format: manifest.Format, Version: manifest.Version, Options: manifest.Options, Capture: manifest.Capture.Digest, Catalog: manifest.Catalog.Digest, Selection: manifest.Selection.Digest})
	if err != nil {
		return manifest, err
	}
	if !bytes.Equal(identity, wantIdentity) {
		return manifest, errors.New("migration archive identity mismatch")
	}
	previousPrefix := 0
	var previousKey []byte
	for index, chunk := range manifest.Chunks {
		if err := ctx.Err(); err != nil {
			return manifest, err
		}
		prefix := 0
		switch chunk.Prefix {
		case "source/":
			prefix = 1
		case "catalog/":
			prefix = 2
		case "selected/":
			prefix = 3
		case "plugin-artifacts/":
			prefix = 4
		}
		if prefix == 0 || prefix < previousPrefix || chunk.Path != fmt.Sprintf("chunks/%08d.jsonl", index) || chunk.Rows == 0 || chunk.Rows > 1000000 || chunk.Bytes == 0 || chunk.Bytes > maxArchiveRecordBytes {
			return manifest, errors.New("invalid migration source chunk descriptor")
		}
		if prefix != previousPrefix {
			previousKey = nil
			previousPrefix = prefix
		}
		data, err := readArchiveObject(ctx, store, chunk.Path, int64(chunk.Bytes))
		if err != nil {
			return manifest, err
		}
		digest := sha256.Sum256(data)
		if uint64(len(data)) != chunk.Bytes || hex.EncodeToString(digest[:]) != chunk.SHA256 {
			return manifest, errors.New("migration source chunk checksum mismatch")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var rows uint64
		for {
			if err := ctx.Err(); err != nil {
				return manifest, err
			}
			var row transfer.SpoolRow
			if err := decoder.Decode(&row); err != nil {
				if err == io.EOF {
					break
				}
				return manifest, err
			}
			rows++
			if rows > chunk.Rows || !bytes.HasPrefix(row.Key, []byte(chunk.Prefix)) || (previousKey != nil && bytes.Compare(previousKey, row.Key) >= 0) {
				return manifest, errors.New("migration source chunk record order or identity mismatch")
			}
			previousKey = bytes.Clone(row.Key)
			if err := visit(row); err != nil {
				return manifest, err
			}
		}
		if rows != chunk.Rows {
			return manifest, errors.New("migration source chunk row count mismatch")
		}
	}
	return manifest, nil
}

func putExactArchiveObject(ctx context.Context, store artifact.ArchiveStore, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := store.Put(ctx, artifact.PutObject{Key: key, Body: bytes.NewReader(data), ExpectedBytes: uint64(len(data)), IfAbsent: true})
	if err == nil {
		return nil
	}
	if !errors.Is(err, artifact.ErrObjectExists) {
		return err
	}
	existing, err := readArchiveObject(ctx, store, key, int64(len(data)))
	if err != nil {
		return err
	}
	if !bytes.Equal(data, existing) {
		return fmt.Errorf("migration archive object conflict: %s", key)
	}
	return nil
}

func readArchiveObject(ctx context.Context, store artifact.ArchiveStore, key string, limit int64) (data []byte, err error) {
	reader, object, err := store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	if object.Bytes > uint64(limit) {
		return nil, errors.New("migration archive object exceeds byte limit")
	}
	data, err = io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit || uint64(len(data)) != object.Bytes {
		return nil, errors.New("migration archive object size mismatch")
	}
	return data, ctx.Err()
}
func decodeArchiveJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing migration archive JSON")
	}
	return nil
}
