package mqttowner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
)

var startedMagic = []byte{'M', 'Q', 'O', 'S', 0, 1}

func (s *Retirements) markerPath(boot string) string {
	key := binary.BigEndian.AppendUint64(nil, s.nodeID)
	key = append(key, boot...)
	sum := sha256.Sum256(key)
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".started")
}

// encodeStartedMarker records that a boot may have issued owners.
func encodeStartedMarker(node uint64, boot string) ([]byte, error) {
	if node == 0 || !contract.ValidIdentity(boot, 128) {
		return nil, errRetirement
	}
	data := bytes.Clone(startedMagic)
	data = binary.BigEndian.AppendUint64(data, node)
	data = binary.BigEndian.AppendUint16(data, uint16(len(boot)))
	data = append(data, boot...)
	sum := sha256.Sum256(data)
	return append(data, sum[:]...), nil
}

// decodeStartedMarker returns the boot only for this node's exact, checksummed
// marker stored under its own name. Anything else fails recovery closed.
func (s *Retirements) decodeStartedMarker(path string) (string, error) {
	data, err := readRetirement(path)
	if err != nil || len(data) < 16+1+sha256.Size || !bytes.Equal(data[:6], startedMagic) {
		return "", errRetirement
	}
	body := data[:len(data)-sha256.Size]
	sum := sha256.Sum256(body)
	if !bytes.Equal(data[len(body):], sum[:]) || binary.BigEndian.Uint64(body[6:14]) != s.nodeID {
		return "", errRetirement
	}
	boot := string(body[16:])
	if int(binary.BigEndian.Uint16(body[14:16])) != len(boot) || !contract.ValidIdentity(boot, 128) || s.markerPath(boot) != path {
		return "", errRetirement
	}
	return boot, nil
}

// Recover must run before any MQTT listener or worker starts and holds an
// exclusive data-directory lock until Close. Under that lock no earlier process
// runs, so each unretired started boot is proven crashed and receives a receipt
// covering every connection ID. The current boot is then marked started.
func (s *Retirements) Recover(ctx context.Context, current string) error {
	if s == nil || ctx == nil || !contract.ValidIdentity(current, 128) {
		return errRetirement
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock != nil || s.closed {
		return errRetirement
	}
	lock, err := lockDir(filepath.Join(s.dir, "LOCK"))
	if err != nil {
		return errRetirement
	}
	if err = s.recoverLocked(ctx, current); err != nil {
		_ = lock.Close()
		return err
	}
	s.lock, s.current = lock, current
	return nil
}

func (s *Retirements) recoverLocked(ctx context.Context, current string) error {
	markers, err := filepath.Glob(filepath.Join(s.dir, "*.started"))
	if err != nil {
		return errRetirement
	}
	for _, path := range markers {
		if err = ctx.Err(); err != nil {
			return err
		}
		boot, err := s.decodeStartedMarker(path)
		if err != nil || boot == current {
			return errRetirement
		}
		if _, statErr := os.Stat(s.path(boot)); errors.Is(statErr, os.ErrNotExist) {
			if err = s.writeReceipt(ctx, boot, math.MaxUint64); err != nil {
				return err
			}
		} else if statErr != nil {
			return errRetirement
		}
		// An existing receipt (graceful or earlier crash) already proves the boot.
		if err = removeDurably(s.dir, path); err != nil {
			return err
		}
	}
	marker, err := encodeStartedMarker(s.nodeID, current)
	if err != nil {
		return err
	}
	return s.publish(ctx, s.markerPath(current), marker)
}

// Close releases the data-directory lock. Callers close only after every MQTT
// producer has joined; a later Recover then treats an unretired boot as crashed.
func (s *Retirements) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	if err != nil {
		return errRetirement
	}
	return nil
}

func removeDurably(dir, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errRetirement
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return errRetirement
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if errors.Join(syncErr, closeErr) != nil {
		return errRetirement
	}
	return nil
}
