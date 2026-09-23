package channel

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"strings"
	"unicode/utf8"
)

// MQTTReplayCopyAuthority preserves the version-1 copy RPC identity. Leases and
// logical retention do not change immutable source content and are excluded.
func MQTTReplayCopyAuthority(m Meta) [32]byte {
	b := binary.BigEndian.AppendUint16([]byte("mqtt-copy-authority-v1"), uint16(len(m.ID.ID)))
	b = append(b, m.ID.ID...)
	b = append(b, byte(m.ID.Type), byte(m.Status))
	for _, v := range []uint64{m.Epoch, m.LeaderEpoch, m.RouteGeneration, uint64(m.Leader), uint64(m.MinISR)} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	for _, nodes := range [][]NodeID{m.Replicas, m.ISR} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(nodes)))
		for _, n := range nodes {
			b = binary.BigEndian.AppendUint64(b, uint64(n))
		}
	}
	return sha256.Sum256(b)
}

// Valid validates a body-free copy interval, including exact row/byte budgets.
// It supplies neither content verification nor membership authority.
func (r MQTTReplayCopyReceipt) Valid() bool {
	q, b, a := r.Request, r.Before, r.After
	if !q.Valid() || len(q.ChannelID.ID) > 1024 || !utf8.ValidString(q.ChannelID.ID) || strings.ContainsRune(q.ChannelID.ID, 0) ||
		r.Leader == 0 || r.Authority == [32]byte{} || r.WriteQuorum < 1 || r.WriteQuorum > 256 || len(r.Copies) > 256 ||
		b.Generation != q.Range.Generation || a.Generation != b.Generation || b.StartAfter != a.StartAfter || b.Through < b.StartAfter ||
		b.Through != q.Range.From-1 || a.Through != q.Range.Through || a.Through-b.Through != uint64(q.Range.Limit) ||
		a.TotalBytes < b.TotalBytes || a.TotalStoredBytes <= b.TotalStoredBytes || a.TotalStoredBytes-b.TotalStoredBytes != uint64(q.Range.MaxBytes) ||
		a.TotalBytes > a.TotalStoredBytes || a.TotalBytes-b.TotalBytes > a.TotalStoredBytes-b.TotalStoredBytes || a.Digest == [32]byte{} {
		return false
	}
	if b.Through == b.StartAfter {
		return b.TotalBytes == 0 && b.TotalStoredBytes == 0 && b.Digest == [32]byte{}
	}
	return b.TotalStoredBytes > 0 && b.TotalBytes <= b.TotalStoredBytes && b.Digest != [32]byte{}
}

// ValidFor verifies distinct ordered current-voter receipts including the leader
// and a strict majority. The caller must obtain m from current authority.
func (r MQTTReplayCopyReceipt) ValidFor(m Meta) bool {
	q := r.Request
	if !r.Valid() || m.ID != q.ChannelID || (m.Key != "" && m.Key != ChannelKeyForID(m.ID)) || m.Epoch != q.ExpectedChannelEpoch || m.LeaderEpoch != q.ExpectedLeaderEpoch || m.RouteGeneration != q.ExpectedRouteGeneration ||
		m.Leader != r.Leader || (m.Status != StatusActive && m.Status != StatusCreating) || m.WriteFence.Set() || len(m.Replicas) == 0 || len(m.Replicas) > 256 || len(m.ISR) == 0 || len(m.ISR) > 256 ||
		m.MinISR != r.WriteQuorum || m.MinISR > len(m.ISR) || m.MinISR*2 <= len(m.ISR) || len(r.Copies) < m.MinISR || len(r.Copies) > len(m.ISR) || MQTTReplayCopyAuthority(m) != r.Authority {
		return false
	}
	for _, nodes := range [][]NodeID{m.Replicas, m.ISR} {
		seen := make(map[NodeID]bool, len(nodes))
		for _, n := range nodes {
			if n == 0 || seen[n] {
				return false
			}
			seen[n] = true
		}
	}
	for _, n := range m.ISR {
		if !slices.Contains(m.Replicas, n) {
			return false
		}
	}
	if !slices.Contains(m.ISR, m.Leader) || !slices.Contains(r.Copies, m.Leader) {
		return false
	}
	for i, n := range r.Copies {
		if !slices.Contains(m.ISR, n) || (i > 0 && r.Copies[i-1] >= n) {
			return false
		}
	}
	return true
}
