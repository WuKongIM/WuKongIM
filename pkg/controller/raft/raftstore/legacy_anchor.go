package raftstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const legacyAnchorFile = "legacy-anchors.json"

// legacyAnchors certifies exact old-format headers only after their retained
// chain has been validated. Persisting before append/deletion makes that proof
// survive WAL-before-metadata crashes and interrupted prefix removal.
type legacyAnchors struct {
	Version  int               `json:"version"`
	NodeID   uint64            `json:"node_id"`
	Headers  map[string]uint32 `json:"headers"`
	Checksum string            `json:"checksum"`
}

func (a legacyAnchors) checksum() string {
	a.Checksum = ""
	data, _ := json.Marshal(a)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func loadLegacyAnchors(dir string, nodeID uint64) (map[string]uint32, error) {
	path := filepath.Join(dir, legacyAnchorFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a legacyAnchors
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("controller/raftstore: invalid legacy anchors: %w", err)
	}
	if a.Version != 1 || a.NodeID != nodeID || a.Checksum != a.checksum() || len(a.Headers) == 0 || len(a.Headers) > 2 {
		return nil, fmt.Errorf("controller/raftstore: legacy anchor identity or checksum mismatch")
	}
	for name := range a.Headers {
		seq, first, err := parseSegmentName(name)
		if err != nil || seq == 0 || first == 0 || name != segmentName(seq, first) {
			return nil, fmt.Errorf("controller/raftstore: invalid legacy anchor segment")
		}
	}
	return a.Headers, nil
}

// rememberLegacyAnchor is called only with a validated successor header. At most
// the current and next legacy prefix remain in the atomically replaced record.
func (w *wal) rememberLegacyAnchor(path string, crc uint32) error {
	headers := make(map[string]uint32, 2)
	for name, value := range w.anchors {
		if _, err := os.Stat(filepath.Join(w.cfg.Dir, name)); err == nil {
			headers[name] = value
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	headers[filepath.Base(path)] = crc
	if len(headers) > 2 {
		return fmt.Errorf("controller/raftstore: too many live legacy anchors")
	}
	a := legacyAnchors{Version: 1, NodeID: w.cfg.NodeID, Headers: headers}
	a.Checksum = a.checksum()
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(w.cfg.Dir, "legacy-anchors.*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(w.cfg.Dir, legacyAnchorFile)); err != nil {
		return err
	}
	if err = syncDir(w.cfg.Dir); err != nil {
		return err
	}
	w.anchors = headers
	return nil
}
