package raftstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.etcd.io/raft/v3/raftpb"
)

type metadata struct {
	Version      int              `json:"version"`
	NodeID       uint64           `json:"node_id"`
	HardState    raftpb.HardState `json:"hard_state"`
	AppliedIndex uint64           `json:"applied_index"`
	Snapshot     snapshotMeta     `json:"snapshot"`
	ConfState    raftpb.ConfState `json:"conf_state"`
}

type snapshotMeta struct {
	Index uint64 `json:"index"`
	Term  uint64 `json:"term"`
	Path  string `json:"path"`
}

func loadMetadata(path string) (metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return metadata{}, nil
		}
		return metadata{}, err
	}
	var meta metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return metadata{}, err
	}
	return meta, nil
}

func saveMetadata(ctx context.Context, path string, meta metadata) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	meta.Version = metadataVersion
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keep = true
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("controllerv2/raftstore: open dir %s: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("controllerv2/raftstore: sync dir %s: %w", dir, err)
	}
	return nil
}
