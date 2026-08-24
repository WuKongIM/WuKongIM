package workload

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestGroupFanoutProofMatchesExactRecipientDelivery(t *testing.T) {
	proof := newDeterministicGroupFanoutProof(t, 3)
	members := []string{"user-1", "user-2", "user-3"}
	proof.ExpectGroup("msg-1", "group-1", "user-1", members)

	for _, recipient := range members[1:] {
		receipt := proof.ObserveGroupRecv(recipient, proofGroupRecv("msg-1", "group-1", "user-1"))
		proof.ObserveRecvACK(receipt, true)
	}

	snapshot := proof.Snapshot()
	if !snapshot.Complete() {
		t.Fatalf("snapshot incomplete: %+v", snapshot)
	}
	if !snapshot.Matches() {
		t.Fatalf("exact recipient delivery did not match: %+v", snapshot)
	}
	if snapshot.LogicalSendACKs != 1 || snapshot.Expected.Count != 2 {
		t.Fatalf("unexpected logical/recipient counts: %+v", snapshot)
	}
}

func TestGroupFanoutProofRejectsMissingRecipientCompensatedByDuplicate(t *testing.T) {
	proof := newDeterministicGroupFanoutProof(t, 3)
	proof.ExpectGroup("msg-1", "group-1", "user-1", []string{"user-1", "user-2", "user-3"})

	for range 2 {
		receipt := proof.ObserveGroupRecv("user-3", proofGroupRecv("msg-1", "group-1", "user-1"))
		proof.ObserveRecvACK(receipt, true)
	}

	snapshot := proof.Snapshot()
	if !snapshot.Complete() {
		t.Fatalf("identity mismatch must remain complete evidence: %+v", snapshot)
	}
	if snapshot.Expected.Count != snapshot.Received.Count || snapshot.Received.Count != snapshot.RecvACKed.Count {
		t.Fatalf("test requires exactly compensating counts: %+v", snapshot)
	}
	if snapshot.Matches() {
		t.Fatalf("missing user-2 plus duplicate user-3 was accepted: %+v", snapshot)
	}
}

func TestGroupFanoutProofLengthPrefixesEveryTupleField(t *testing.T) {
	proof := newDeterministicGroupFanoutProof(t, 2)
	proof.ExpectGroup("ab", "c", "sender", []string{"recipient", "sender"})

	// Without length prefixes, client_msg_no="ab" + channel_id="c" is the
	// same byte stream as client_msg_no="a" + channel_id="bc".
	receipt := proof.ObserveGroupRecv("recipient", proofGroupRecv("a", "bc", "sender"))
	proof.ObserveRecvACK(receipt, true)

	snapshot := proof.Snapshot()
	if !snapshot.Complete() || snapshot.Expected.Count != snapshot.Received.Count {
		t.Fatalf("test requires complete equal-count evidence: %+v", snapshot)
	}
	if snapshot.Matches() {
		t.Fatalf("ambiguous field concatenation matched: %+v", snapshot)
	}
}

func TestGroupFanoutProofIsOrderIndependentUnderConcurrency(t *testing.T) {
	sequential := newDeterministicGroupFanoutProof(t, 3)
	concurrent := newDeterministicGroupFanoutProof(t, 3)
	members := []string{"user-3", "user-1", "user-2"}

	for index := range 128 {
		message := fanoutTestMessage(index)
		sequential.ExpectGroup(message, "group-1", "user-1", members)
		for _, recipient := range []string{"user-2", "user-3"} {
			receipt := sequential.ObserveGroupRecv(recipient, proofGroupRecv(message, "group-1", "user-1"))
			sequential.ObserveRecvACK(receipt, true)
		}
	}

	var wait sync.WaitGroup
	for index := 127; index >= 0; index-- {
		message := fanoutTestMessage(index)
		wait.Add(1)
		go func() {
			defer wait.Done()
			concurrent.ExpectGroup(message, "group-1", "user-1", members)
			for _, recipient := range []string{"user-3", "user-2"} {
				receipt := concurrent.ObserveGroupRecv(recipient, proofGroupRecv(message, "group-1", "user-1"))
				concurrent.ObserveRecvACK(receipt, true)
			}
		}()
	}
	wait.Wait()

	want, got := sequential.Snapshot(), concurrent.Snapshot()
	if want != got {
		t.Fatalf("event order changed the proof:\nwant %+v\n got %+v", want, got)
	}
	if !got.Matches() {
		t.Fatalf("concurrent exact delivery did not match: %+v", got)
	}
}

func TestGroupFanoutProofFailedRecvACKDoesNotJoinAcknowledgedMultiset(t *testing.T) {
	proof := newDeterministicGroupFanoutProof(t, 2)
	proof.ExpectGroup("msg-1", "group-1", "user-1", []string{"user-1", "user-2"})
	receipt := proof.ObserveGroupRecv("user-2", proofGroupRecv("msg-1", "group-1", "user-1"))
	proof.ObserveRecvACK(receipt, false)

	snapshot := proof.Snapshot()
	if !snapshot.Complete() {
		t.Fatalf("failed writer result should be represented as a short ACK set: %+v", snapshot)
	}
	if snapshot.Received.Count != 1 || snapshot.RecvACKed.Count != 0 || snapshot.Matches() {
		t.Fatalf("failed RECVACK entered acknowledged multiset: %+v", snapshot)
	}
}

func TestGroupFanoutProofRecordsUnexpectedSelfDeliveryAsCompleteMismatch(t *testing.T) {
	proof := newDeterministicGroupFanoutProof(t, 2)
	proof.ExpectGroup("msg-1", "group-1", "user-1", []string{"user-1", "user-2"})

	receipt := proof.ObserveGroupRecv("user-1", proofGroupRecv("msg-1", "group-1", "user-1"))
	proof.ObserveRecvACK(receipt, true)

	snapshot := proof.Snapshot()
	if !snapshot.Complete() {
		t.Fatalf("unexpected self-delivery must remain complete physical evidence: %+v", snapshot)
	}
	if snapshot.Expected.Count != 1 || snapshot.Received.Count != 1 || snapshot.RecvACKed.Count != 1 {
		t.Fatalf("unexpected self-delivery and its successful RECVACK were not recorded: %+v", snapshot)
	}
	if snapshot.Matches() {
		t.Fatalf("unexpected sender self-delivery matched the expected recipient: %+v", snapshot)
	}
}

func TestGroupFanoutProofSnapshotDoesNotExposeRawIdentities(t *testing.T) {
	proof := newDeterministicGroupFanoutProof(t, 2)
	proof.ExpectGroup("secret-message-number", "private-channel", "sender-private", []string{"recipient-private", "sender-private"})
	receipt := proof.ObserveGroupRecv("recipient-private", proofGroupRecv("secret-message-number", "private-channel", "sender-private"))
	proof.ObserveRecvACK(receipt, true)

	body, err := json.Marshal(proof.Snapshot())
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, identity := range []string{"secret-message-number", "private-channel", "sender-private", "recipient-private"} {
		if strings.Contains(string(body), identity) {
			t.Fatalf("snapshot exposed raw identity %q: %s", identity, body)
		}
	}
}

func TestGroupFanoutProofInvalidInputsPermanentlyInvalidateEvidence(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*GroupFanoutProof)
	}{
		{
			name: "empty message identity",
			invoke: func(proof *GroupFanoutProof) {
				proof.ExpectGroup("", "group-1", "user-1", []string{"user-1", "user-2"})
			},
		},
		{
			name: "duplicate member",
			invoke: func(proof *GroupFanoutProof) {
				proof.ExpectGroup("msg-1", "group-1", "user-1", []string{"user-1", "user-1"})
			},
		},
		{
			name: "sender absent",
			invoke: func(proof *GroupFanoutProof) {
				proof.ExpectGroup("msg-1", "group-1", "user-1", []string{"user-2", "user-3"})
			},
		},
		{
			name: "wrong member count",
			invoke: func(proof *GroupFanoutProof) {
				proof.ExpectGroup("msg-1", "group-1", "user-1", []string{"user-1"})
			},
		},
		{
			name: "nil recv",
			invoke: func(proof *GroupFanoutProof) {
				proof.ObserveGroupRecv("user-2", nil)
			},
		},
		{
			name: "non-group recv",
			invoke: func(proof *GroupFanoutProof) {
				recv := proofGroupRecv("msg-1", "group-1", "user-1")
				recv.ChannelType = frame.ChannelTypePerson
				proof.ObserveGroupRecv("user-2", recv)
			},
		},
		{
			name: "invalid successful ack receipt",
			invoke: func(proof *GroupFanoutProof) {
				proof.ObserveRecvACK(FanoutReceipt{}, true)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proof := newDeterministicGroupFanoutProof(t, 2)
			test.invoke(proof)
			if proof.Snapshot().EvidenceComplete {
				t.Fatal("invalid input left evidence complete")
			}
			proof.ExpectGroup("msg-valid", "group-valid", "user-1", []string{"user-1", "user-2"})
			if proof.Snapshot().EvidenceComplete {
				t.Fatal("later valid evidence repaired a permanent invalidation")
			}
		})
	}
}

func TestGroupFanoutProofRejectsReceiptFromAnotherAssignmentProof(t *testing.T) {
	first := newDeterministicGroupFanoutProof(t, 2)
	var otherSecret [32]byte
	copy(otherSecret[:], "another-assignment-proof-secret")
	second, err := newGroupFanoutProofWithSecret(2, otherSecret)
	if err != nil {
		t.Fatalf("new second proof: %v", err)
	}
	receipt := first.ObserveGroupRecv("user-2", proofGroupRecv("msg-1", "group-1", "user-1"))
	second.ObserveRecvACK(receipt, true)
	if second.Snapshot().EvidenceComplete {
		t.Fatal("cross-assignment receipt left evidence complete")
	}
}

func TestGroupFanoutProofCountOverflowPermanentlyInvalidatesEvidence(t *testing.T) {
	proof := newDeterministicGroupFanoutProof(t, 2)
	projection := proof.project("msg-1", "group-1", frame.ChannelTypeGroup, "user-1", "user-2")
	stripe := proof.stripe(projection)
	stripe.expected.count = math.MaxUint64

	proof.ExpectGroup("msg-1", "group-1", "user-1", []string{"user-1", "user-2"})
	if proof.Snapshot().EvidenceComplete {
		t.Fatal("count overflow left evidence complete")
	}
	stripe.expected.count = 0
	if proof.Snapshot().EvidenceComplete {
		t.Fatal("removing the synthetic overflow repaired permanent invalidation")
	}
}

func TestGroupFanoutProofLogicalSendACKOverflowPermanentlyInvalidatesEvidence(t *testing.T) {
	proof := newDeterministicGroupFanoutProof(t, 2)
	proof.logicalSendACKs = math.MaxUint64
	proof.ExpectGroup("msg-1", "group-1", "user-1", []string{"user-1", "user-2"})
	if proof.Snapshot().EvidenceComplete {
		t.Fatal("logical SENDACK overflow left evidence complete")
	}
}

func TestNewGroupFanoutProofRejectsInvalidShapeAndSecret(t *testing.T) {
	if _, err := NewGroupFanoutProof(1); err == nil {
		t.Fatal("one-member group proof was accepted")
	}
	if _, err := newGroupFanoutProofWithSecret(2, [32]byte{}); err == nil {
		t.Fatal("zero deterministic secret was accepted")
	}
}

func fanoutTestMessage(index int) string {
	const digits = "0123456789abcdef"
	return "message-" + string([]byte{digits[(index>>4)&15], digits[index&15]})
}

func newDeterministicGroupFanoutProof(t *testing.T, groupMembers int) *GroupFanoutProof {
	t.Helper()
	var secret [32]byte
	copy(secret[:], "deterministic-fanout-proof-key")
	proof, err := newGroupFanoutProofWithSecret(groupMembers, secret)
	if err != nil {
		t.Fatalf("new proof: %v", err)
	}
	return proof
}

func proofGroupRecv(clientMsgNo, channelID, senderUID string) *frame.RecvPacket {
	return &frame.RecvPacket{
		ClientMsgNo: clientMsgNo,
		ChannelID:   channelID,
		ChannelType: frame.ChannelTypeGroup,
		FromUID:     senderUID,
	}
}
