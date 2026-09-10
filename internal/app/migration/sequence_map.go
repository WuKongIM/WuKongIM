package migration

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	usecase "github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

// sequenceMapFile publishes a bounded, lossless sidecar for client migration.
// Its digest covers the exact JSONL bytes and its selection identifies the
// generation; no client is assumed to have consumed or applied the mapping.
type sequenceMapFile struct {
	ChainProof      *sequenceMapFile `json:"duplicate_chain_proof,omitempty"`
	Path            string           `json:"path"`
	SHA256          string           `json:"sha256"`
	Rows            uint64           `json:"rows"`
	SelectionDigest string           `json:"selection_digest"`
}

func writeSequenceMap(ctx context.Context, dir string, w usecase.Workspace, p usecase.Preflight) (out *sequenceMapFile, err error) {
	if p.Conversion.Transformation == nil {
		return nil, nil
	}
	out, err = writeMappingSidecar(ctx, dir, w, p, "sequence-map", usecase.WalkMessageSequenceMappings)
	if err != nil {
		return nil, err
	}
	if p.Conversion.Transformation.ChainRoots > 0 {
		out.ChainProof, err = writeMappingSidecar(ctx, dir, w, p, "duplicate-chains", func(ctx context.Context, w usecase.Workspace, visit func(usecase.MessageSequenceMapping) error) error {
			return usecase.WalkDuplicateChainMappings(ctx, w, p.Conversion.Transformation, visit)
		})
		if err != nil {
			return nil, err
		}
		if out.ChainProof.Rows != p.Conversion.Transformation.ChainRoots {
			return nil, errors.New("duplicate chain sidecar count differs from preparation")
		}
	}
	return out, nil
}

func writeMappingSidecar(ctx context.Context, dir string, w usecase.Workspace, p usecase.Preflight, name string, walk func(context.Context, usecase.Workspace, func(usecase.MessageSequenceMapping) error) error) (out *sequenceMapFile, err error) {
	f, err := os.CreateTemp(dir, "."+name+"-incomplete-*.jsonl")
	if err != nil {
		return nil, err
	}
	out = &sequenceMapFile{SelectionDigest: p.Selection.Digest}
	buffer := bufio.NewWriterSize(f, 64<<10)
	h := sha256.New()
	writer := io.MultiWriter(buffer, h)
	runErr := walk(ctx, w, func(row usecase.MessageSequenceMapping) error {
		data, err := usecase.MarshalState(row)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return err
		}
		out.Rows++
		return nil
	})
	if err := errors.Join(runErr, buffer.Flush(), f.Sync(), f.Close()); err != nil {
		return nil, err
	}
	out.SHA256 = hex.EncodeToString(h.Sum(nil))
	out.Path = filepath.Join(dir, name+"-"+out.SHA256+".jsonl")
	if err := os.Rename(f.Name(), out.Path); err != nil {
		return nil, err
	}
	return out, nil
}
