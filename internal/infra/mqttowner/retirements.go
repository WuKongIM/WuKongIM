// Package mqttowner persists node-local proofs of graceful MQTT owner retirement.
package mqttowner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
)

const retirementFileLimit = 1024

var errRetirement = errors.New("mqttowner: retirement persistence failed")

// Retirements keeps one immutable, bounded file per proved boot, not per client.
// Point reads need no past-boot scan or cache. App owns the node data directory.
type Retirements struct {
	dir    string
	nodeID uint64
}

func NewRetirements(dir string, nodeID uint64) (*Retirements, error) {
	if dir == "" || nodeID == 0 {
		return nil, errRetirement
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errRetirement
	}
	return &Retirements{dir: dir, nodeID: nodeID}, nil
}

func (s *Retirements) path(boot string) string {
	key := binary.BigEndian.AppendUint64(nil, s.nodeID)
	key = append(key, boot...)
	sum := sha256.Sum256(key)
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".receipt")
}

// Record publishes only a runtime-minted terminal proof. A failed/uncertain
// publication can be retried; conflicting facts are never overwritten.
func (s *Retirements) Record(ctx context.Context, proof runtime.RetiredBoot) error {
	if s == nil || ctx == nil {
		return errRetirement
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	node, boot, maxID := proof.Identity()
	if node != s.nodeID || !contract.ValidIdentity(boot, 128) {
		return errRetirement
	}
	if maxID == 0 {
		return nil
	}
	data := []byte{'M', 'Q', 'O', 'R', 0, 1}
	data = binary.BigEndian.AppendUint64(data, node)
	data = binary.BigEndian.AppendUint64(data, maxID)
	data = binary.BigEndian.AppendUint16(data, uint16(len(boot)))
	data = append(data, boot...)
	sum := sha256.Sum256(data)
	data = append(data, sum[:]...)
	f, err := os.CreateTemp(s.dir, ".retirement-")
	if err != nil {
		return errRetirement
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		return errRetirement
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	path := s.path(boot)
	if err = os.Link(f.Name(), path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return errRetirement
		}
		existing, readErr := readRetirement(path)
		if readErr != nil || !bytes.Equal(existing, data) {
			return errRetirement
		}
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return errRetirement
	}
	syncErr = dir.Sync()
	closeErr = dir.Close()
	if errors.Join(syncErr, closeErr) != nil {
		return errRetirement
	}
	return ctx.Err()
}

// Quiesce accepts only this node's exact boot and issued-ID bound. A valid
// receipt remains true regardless of later process failure or elapsed time.
func (s *Retirements) Quiesce(ctx context.Context, o contract.Owner) error {
	if s == nil || ctx == nil {
		return runtime.ErrOwnerInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if o.Validate() != nil || o.NodeID != s.nodeID {
		return runtime.ErrOwnerUnknown
	}
	data, err := readRetirement(s.path(o.BootID))
	if err != nil {
		return runtime.ErrOwnerUnknown
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if len(data) < 24+1+sha256.Size || !bytes.Equal(data[:6], []byte{'M', 'Q', 'O', 'R', 0, 1}) {
		return runtime.ErrOwnerUnknown
	}
	body := data[:len(data)-sha256.Size]
	sum := sha256.Sum256(body)
	if !bytes.Equal(data[len(body):], sum[:]) {
		return runtime.ErrOwnerUnknown
	}
	bootLen := int(binary.BigEndian.Uint16(body[22:24]))
	if bootLen != len(body)-24 || string(body[24:]) != o.BootID || binary.BigEndian.Uint64(body[6:14]) != o.NodeID || o.ConnectionID > binary.BigEndian.Uint64(body[14:22]) {
		return runtime.ErrOwnerUnknown
	}
	return nil
}

func readRetirement(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > retirementFileLimit {
		return nil, errRetirement
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errRetirement
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, retirementFileLimit+1))
	if err != nil || len(data) > retirementFileLimit {
		return nil, errRetirement
	}
	return data, nil
}
