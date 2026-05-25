package raftstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.etcd.io/raft/v3/raftpb"
)

type snapshotEnvelope struct {
	Version  int                     `json:"version"`
	Metadata raftpb.SnapshotMetadata `json:"metadata"`
	Data     []byte                  `json:"data"`
	Checksum string                  `json:"checksum"`
}

func saveSnapshotFile(ctx context.Context, dir string, snap raftpb.Snapshot) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	envelope := snapshotEnvelope{Version: snapshotVersion, Metadata: cloneSnapshotMetadata(snap.Metadata), Data: append([]byte(nil), snap.Data...)}
	sum := sha256.Sum256(snap.Data)
	envelope.Checksum = hex.EncodeToString(sum[:])
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	name := snapshotFileName(snap.Metadata.Index, snap.Metadata.Term)
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return "", err
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
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	finalPath := filepath.Join(dir, name)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", err
	}
	keep = true
	if err := syncDir(dir); err != nil {
		return "", err
	}
	return finalPath, nil
}

func loadSnapshotFile(path string) (raftpb.Snapshot, error) {
	if path == "" {
		return raftpb.Snapshot{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return raftpb.Snapshot{}, nil
		}
		return raftpb.Snapshot{}, err
	}
	var envelope snapshotEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return raftpb.Snapshot{}, err
	}
	sum := sha256.Sum256(envelope.Data)
	if got := hex.EncodeToString(sum[:]); envelope.Checksum != "" && got != envelope.Checksum {
		return raftpb.Snapshot{}, fmt.Errorf("controllerv2/raftstore: snapshot checksum mismatch")
	}
	return raftpb.Snapshot{Data: append([]byte(nil), envelope.Data...), Metadata: cloneSnapshotMetadata(envelope.Metadata)}, nil
}

func snapshotFileName(index, term uint64) string {
	return fmt.Sprintf("%016x-%016x.snap", index, term)
}
