package model

const (
	// FanoutProofVersion identifies the fixed group-recipient multiset proof
	// understood by reviewed wkbench consumers.
	FanoutProofVersion = "wukongim/group-fanout-proof/v1"
	fanoutDigestHexLen = 64
	fanoutZeroDigest   = "0000000000000000000000000000000000000000000000000000000000000000"
)

// FanoutMultisetSummary is a fixed-size, identity-free projection of one
// recipient-delivery multiset. DigestA and DigestB are independent keyed
// 256-bit additive projections encoded as lowercase hexadecimal.
type FanoutMultisetSummary struct {
	Count   uint64 `json:"count"`
	DigestA string `json:"digest_a"`
	DigestB string `json:"digest_b"`
}

// FanoutProofSnapshot compares the recipient identities expected after
// successful logical SENDACKs with physical RECV and successful RECVACK work.
// It never contains user, channel, or message identities.
type FanoutProofSnapshot struct {
	Version          string                `json:"version"`
	Required         bool                  `json:"required"`
	EvidenceComplete bool                  `json:"evidence_complete"`
	LogicalSendACKs  uint64                `json:"logical_sendacks"`
	Expected         FanoutMultisetSummary `json:"expected"`
	Received         FanoutMultisetSummary `json:"received"`
	RecvACKed        FanoutMultisetSummary `json:"recvacked"`
}

// Complete reports whether the snapshot has the closed schema and two
// canonical digest lanes for every multiset. It does not claim equality.
func (s FanoutProofSnapshot) Complete() bool {
	if s.Version != FanoutProofVersion || !s.EvidenceComplete ||
		!s.Expected.valid() || !s.Received.valid() || !s.RecvACKed.valid() {
		return false
	}
	if !s.Required {
		return s == FanoutProofNotRequired()
	}
	return true
}

// Matches reports whether complete expected, received, and successfully
// acknowledged recipient multisets are exactly equal in both keyed lanes.
func (s FanoutProofSnapshot) Matches() bool {
	return s.Complete() && s.Expected == s.Received && s.Expected == s.RecvACKed
}

// FanoutProofNotRequired returns the sole complete proof shape for an
// assignment that does not require reviewed group-recipient accounting.
func FanoutProofNotRequired() FanoutProofSnapshot {
	zero := FanoutMultisetSummary{DigestA: fanoutZeroDigest, DigestB: fanoutZeroDigest}
	return FanoutProofSnapshot{
		Version:          FanoutProofVersion,
		EvidenceComplete: true,
		Expected:         zero,
		Received:         zero,
		RecvACKed:        zero,
	}
}

func (s FanoutMultisetSummary) valid() bool {
	if !validFanoutDigest(s.DigestA) || !validFanoutDigest(s.DigestB) {
		return false
	}
	return s.Count != 0 || (s.DigestA == fanoutZeroDigest && s.DigestB == fanoutZeroDigest)
}

func validFanoutDigest(value string) bool {
	if len(value) != fanoutDigestHexLen {
		return false
	}
	for idx := 0; idx < len(value); idx++ {
		b := value[idx]
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}
