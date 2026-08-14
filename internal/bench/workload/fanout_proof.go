package workload

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/bits"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	groupFanoutProofStripes = 64
	groupFanoutSecretBytes  = 32
)

// FanoutReceipt is an opaque, identity-free handle for one valid physical
// group RECV. A successful RECVACK reuses its keyed projection without
// retaining or re-encoding the original recipient identity.
type FanoutReceipt struct {
	proofID [16]byte
	digestA fanoutUint256
	digestB fanoutUint256
	valid   bool
}

// GroupFanoutProof is a fixed-retained-memory assignment-level proof. Its 64
// stripes make concurrent receive observation bounded without a global hot
// lock; no recipient, channel, sender, or message identity is retained.
type GroupFanoutProof struct {
	groupMembers int
	keyA         [groupFanoutSecretBytes]byte
	keyB         [groupFanoutSecretBytes]byte
	proofID      [16]byte

	evidenceComplete atomic.Bool
	logicalMu        sync.Mutex
	logicalSendACKs  uint64
	stripes          [groupFanoutProofStripes]fanoutProofStripe
}

type fanoutProofStripe struct {
	mu        sync.Mutex
	expected  fanoutAccumulator
	received  fanoutAccumulator
	recvACKed fanoutAccumulator
}

type fanoutAccumulator struct {
	count   uint64
	digestA fanoutUint256
	digestB fanoutUint256
}

// fanoutUint256 stores an unsigned 256-bit integer as little-endian limbs.
// Addition intentionally wraps modulo 2^256; two separately keyed lanes make
// equal-count missing-plus-duplicate compensation cryptographically bounded.
type fanoutUint256 [4]uint64

type fanoutProjection struct {
	digestA fanoutUint256
	digestB fanoutUint256
}

// NewGroupFanoutProof creates a proof with a fresh assignment-local secret.
func NewGroupFanoutProof(groupMembers int) (*GroupFanoutProof, error) {
	var secret [groupFanoutSecretBytes]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, fmt.Errorf("group fanout proof secret: %w", err)
	}
	return newGroupFanoutProofWithSecret(groupMembers, secret)
}

// newGroupFanoutProofWithSecret is the deterministic test seam. Production
// callers must use NewGroupFanoutProof so evidence cannot be algebraically
// tailored to a published key.
func newGroupFanoutProofWithSecret(groupMembers int, secret [groupFanoutSecretBytes]byte) (*GroupFanoutProof, error) {
	if groupMembers < 2 {
		return nil, fmt.Errorf("group fanout proof requires at least two group members")
	}
	if secret == ([groupFanoutSecretBytes]byte{}) {
		return nil, fmt.Errorf("group fanout proof secret must not be zero")
	}
	proof := &GroupFanoutProof{
		groupMembers: groupMembers,
		keyA:         deriveFanoutKey(secret, "digest-a"),
		keyB:         deriveFanoutKey(secret, "digest-b"),
	}
	proofID := deriveFanoutKey(secret, "receipt-owner")
	copy(proof.proofID[:], proofID[:len(proof.proofID)])
	proof.evidenceComplete.Store(true)
	return proof, nil
}

// ExpectGroup records the recipient identities implied by one successful
// logical group SENDACK. Members must contain the configured complete group
// membership exactly once, including senderUID.
func (p *GroupFanoutProof) ExpectGroup(clientMsgNo, channelID, senderUID string, members []string) {
	if p == nil {
		return
	}
	if !validFanoutField(clientMsgNo) || !validFanoutField(channelID) || !validFanoutField(senderUID) ||
		len(members) != p.groupMembers || !validFanoutMembers(members, senderUID) {
		p.invalidate()
		return
	}
	if !p.incrementLogicalSendACKs() {
		return
	}
	for _, recipientUID := range members {
		if recipientUID == senderUID {
			continue
		}
		projection := p.project(clientMsgNo, channelID, frame.ChannelTypeGroup, senderUID, recipientUID)
		if !p.addExpected(projection) {
			return
		}
	}
}

// ObserveGroupRecv records one physical group RECV and returns the opaque
// projection required to bind a later successful RECVACK to that same event.
func (p *GroupFanoutProof) ObserveGroupRecv(recipientUID string, recv *frame.RecvPacket) FanoutReceipt {
	if p == nil {
		return FanoutReceipt{}
	}
	if recv == nil || !validFanoutField(recipientUID) || !validFanoutField(recv.ClientMsgNo) ||
		!validFanoutField(recv.ChannelID) || !validFanoutField(recv.FromUID) ||
		recv.ChannelType != frame.ChannelTypeGroup {
		p.invalidate()
		return FanoutReceipt{}
	}
	// An unexpected delivery back to the sender is still a well-formed physical
	// tuple. Preserve it so the complete received multiset mismatches expected.
	projection := p.project(recv.ClientMsgNo, recv.ChannelID, recv.ChannelType, recv.FromUID, recipientUID)
	if !p.addReceived(projection) {
		return FanoutReceipt{}
	}
	return FanoutReceipt{
		proofID: p.proofID,
		digestA: projection.digestA,
		digestB: projection.digestB,
		valid:   true,
	}
}

// ObserveRecvACK adds a receipt only after the protocol RECVACK writer reports
// success. A failed write deliberately leaves the acknowledged multiset short.
func (p *GroupFanoutProof) ObserveRecvACK(receipt FanoutReceipt, success bool) {
	if p == nil || !success {
		return
	}
	if !receipt.valid || subtle.ConstantTimeCompare(receipt.proofID[:], p.proofID[:]) != 1 {
		p.invalidate()
		return
	}
	p.addRecvACKed(fanoutProjection{digestA: receipt.digestA, digestB: receipt.digestB})
}

// Snapshot returns the fixed-size, identity-free assignment proof. It is safe
// during traffic, although only a terminal cut can establish final equality.
func (p *GroupFanoutProof) Snapshot() model.FanoutProofSnapshot {
	if p == nil {
		return emptyFanoutProofSnapshot(false)
	}
	snapshot := emptyFanoutProofSnapshot(true)
	p.logicalMu.Lock()
	snapshot.LogicalSendACKs = p.logicalSendACKs
	p.logicalMu.Unlock()

	var expected, received, recvACKed fanoutAccumulator
	complete := p.evidenceComplete.Load()
	for idx := range p.stripes {
		stripe := &p.stripes[idx]
		stripe.mu.Lock()
		if !mergeFanoutAccumulator(&expected, stripe.expected) {
			complete = false
		}
		if !mergeFanoutAccumulator(&received, stripe.received) {
			complete = false
		}
		if !mergeFanoutAccumulator(&recvACKed, stripe.recvACKed) {
			complete = false
		}
		stripe.mu.Unlock()
	}
	if !complete {
		p.invalidate()
	}
	snapshot.Expected = expected.summary()
	snapshot.Received = received.summary()
	snapshot.RecvACKed = recvACKed.summary()
	snapshot.EvidenceComplete = complete && p.evidenceComplete.Load()
	return snapshot
}

func emptyFanoutProofSnapshot(required bool) model.FanoutProofSnapshot {
	zero := (fanoutUint256{}).hex()
	return model.FanoutProofSnapshot{
		Version:   model.FanoutProofVersion,
		Required:  required,
		Expected:  model.FanoutMultisetSummary{DigestA: zero, DigestB: zero},
		Received:  model.FanoutMultisetSummary{DigestA: zero, DigestB: zero},
		RecvACKed: model.FanoutMultisetSummary{DigestA: zero, DigestB: zero},
	}
}

func deriveFanoutKey(secret [groupFanoutSecretBytes]byte, label string) [groupFanoutSecretBytes]byte {
	mac := hmac.New(sha256.New, secret[:])
	_, _ = mac.Write([]byte(model.FanoutProofVersion))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(label))
	var result [groupFanoutSecretBytes]byte
	copy(result[:], mac.Sum(nil))
	return result
}

func (p *GroupFanoutProof) project(clientMsgNo, channelID string, channelType uint8, senderUID, recipientUID string) fanoutProjection {
	return fanoutProjection{
		digestA: fanoutDigest(p.keyA, clientMsgNo, channelID, channelType, senderUID, recipientUID),
		digestB: fanoutDigest(p.keyB, clientMsgNo, channelID, channelType, senderUID, recipientUID),
	}
}

func fanoutDigest(key [groupFanoutSecretBytes]byte, clientMsgNo, channelID string, channelType uint8, senderUID, recipientUID string) fanoutUint256 {
	mac := hmac.New(sha256.New, key[:])
	writeFanoutField(mac, []byte(model.FanoutProofVersion))
	writeFanoutField(mac, []byte(clientMsgNo))
	writeFanoutField(mac, []byte(channelID))
	writeFanoutField(mac, []byte{channelType})
	writeFanoutField(mac, []byte(senderUID))
	writeFanoutField(mac, []byte(recipientUID))
	return fanoutUint256FromBytes(mac.Sum(nil))
}

type fanoutWriter interface {
	Write([]byte) (int, error)
}

func writeFanoutField(writer fanoutWriter, value []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(value)
}

func validFanoutField(value string) bool {
	return value != "" && uint64(len(value)) <= math.MaxUint32
}

func validFanoutMembers(members []string, senderUID string) bool {
	senderSeen := false
	ordered := true
	for idx, member := range members {
		if !validFanoutField(member) {
			return false
		}
		if member == senderUID {
			if senderSeen {
				return false
			}
			senderSeen = true
		}
		if idx > 0 {
			comparison := strings.Compare(members[idx-1], member)
			if comparison == 0 {
				return false
			}
			if comparison > 0 {
				ordered = false
			}
		}
	}
	if !senderSeen {
		return false
	}
	if ordered {
		return true
	}
	// Production GroupChannel members are normalized into sorted unique order,
	// so the high-scale path above retains O(1) temporary memory. This fallback
	// preserves the public seam for arbitrary caller order without retaining a
	// membership map in the assignment proof.
	seen := make(map[string]struct{}, len(members))
	for _, member := range members {
		if _, exists := seen[member]; exists {
			return false
		}
		seen[member] = struct{}{}
	}
	return true
}

func (p *GroupFanoutProof) incrementLogicalSendACKs() bool {
	p.logicalMu.Lock()
	defer p.logicalMu.Unlock()
	if p.logicalSendACKs == math.MaxUint64 {
		p.invalidate()
		return false
	}
	p.logicalSendACKs++
	return true
}

func (p *GroupFanoutProof) addExpected(projection fanoutProjection) bool {
	stripe := p.stripe(projection)
	stripe.mu.Lock()
	defer stripe.mu.Unlock()
	return p.addAccumulator(&stripe.expected, projection)
}

func (p *GroupFanoutProof) addReceived(projection fanoutProjection) bool {
	stripe := p.stripe(projection)
	stripe.mu.Lock()
	defer stripe.mu.Unlock()
	return p.addAccumulator(&stripe.received, projection)
}

func (p *GroupFanoutProof) addRecvACKed(projection fanoutProjection) bool {
	stripe := p.stripe(projection)
	stripe.mu.Lock()
	defer stripe.mu.Unlock()
	return p.addAccumulator(&stripe.recvACKed, projection)
}

func (p *GroupFanoutProof) stripe(projection fanoutProjection) *fanoutProofStripe {
	return &p.stripes[projection.digestA[0]&(groupFanoutProofStripes-1)]
}

func (p *GroupFanoutProof) addAccumulator(accumulator *fanoutAccumulator, projection fanoutProjection) bool {
	if accumulator.count == math.MaxUint64 {
		p.invalidate()
		return false
	}
	accumulator.count++
	accumulator.digestA.add(projection.digestA)
	accumulator.digestB.add(projection.digestB)
	return true
}

func (p *GroupFanoutProof) invalidate() {
	if p != nil {
		p.evidenceComplete.Store(false)
	}
}

func mergeFanoutAccumulator(target *fanoutAccumulator, source fanoutAccumulator) bool {
	count, carry := bits.Add64(target.count, source.count, 0)
	target.count = count
	target.digestA.add(source.digestA)
	target.digestB.add(source.digestB)
	return carry == 0
}

func (a fanoutAccumulator) summary() model.FanoutMultisetSummary {
	return model.FanoutMultisetSummary{
		Count:   a.count,
		DigestA: a.digestA.hex(),
		DigestB: a.digestB.hex(),
	}
}

func fanoutUint256FromBytes(value []byte) fanoutUint256 {
	var result fanoutUint256
	for idx := 0; idx < len(result); idx++ {
		result[len(result)-1-idx] = binary.BigEndian.Uint64(value[idx*8 : (idx+1)*8])
	}
	return result
}

func (value *fanoutUint256) add(other fanoutUint256) {
	var carry uint64
	for idx := range value {
		value[idx], carry = bits.Add64(value[idx], other[idx], carry)
	}
}

func (value fanoutUint256) hex() string {
	var encoded [32]byte
	for idx := 0; idx < len(value); idx++ {
		binary.BigEndian.PutUint64(encoded[idx*8:(idx+1)*8], value[len(value)-1-idx])
	}
	return hex.EncodeToString(encoded[:])
}
