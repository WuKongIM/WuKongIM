package handler

import "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"

type seqDirection uint8

const (
	seqDirectionForward seqDirection = iota
	seqDirectionReverse
)

// seqBounds captures the committed and retention-visible inclusive sequence window.
type seqBounds struct {
	min uint64
	max uint64
}

func newSeqBounds(committedHW, minAvailableSeq uint64) (seqBounds, bool) {
	minAvailableSeq = normalizeMinAvailableSeq(minAvailableSeq)
	if committedHW < minAvailableSeq {
		return seqBounds{}, false
	}
	return seqBounds{min: minAvailableSeq, max: committedHW}, true
}

func (b seqBounds) contains(seq uint64) bool {
	return seq >= b.min && seq <= b.max
}

// exclusiveUpper returns an exclusive upper bound; 0 means open-ended for store scans.
func (b seqBounds) exclusiveUpper(defaultOpen bool, requested uint64) uint64 {
	if requested == 0 || requested > b.max {
		if defaultOpen && b.max == ^uint64(0) {
			return 0
		}
		return b.max + 1
	}
	return requested
}

// appendBoundedMessages copies already-scanned messages while enforcing commit
// and retention bounds. It returns true when it observed one item beyond limit.
func appendBoundedMessages(dst []channel.Message, messages []channel.Message, bounds seqBounds, limit int, direction seqDirection, stop func(channel.Message) bool) ([]channel.Message, bool) {
	for _, msg := range messages {
		if msg.MessageSeq == 0 || msg.MessageSeq > bounds.max {
			continue
		}
		if msg.MessageSeq < bounds.min {
			if direction == seqDirectionReverse {
				break
			}
			continue
		}
		if stop != nil && stop(msg) {
			break
		}
		dst = append(dst, msg)
		if len(dst) > limit {
			return dst, true
		}
	}
	return dst, len(dst) > limit
}

func trimLimit(messages []channel.Message, limit int) ([]channel.Message, bool) {
	if len(messages) <= limit {
		return messages, false
	}
	return messages[:limit], true
}

func normalizeMinAvailableSeq(seq uint64) uint64 {
	if seq == 0 {
		return 1
	}
	return seq
}

func reverseMessages(messages []channel.Message) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
