package message

import "hash/maphash"

const (
	idempotencyMembershipPrimaryWords    = 64
	idempotencyMembershipOverflowWords   = 128
	idempotencyMembershipPrimaryCapacity = 384
	idempotencyMembershipHashCount       = 7
	idempotencyMembershipSparseKeys      = 8
)

var (
	idempotencyMembershipSeed1 = maphash.MakeSeed()
	idempotencyMembershipSeed2 = maphash.MakeSeed()
)

// idempotencyMembershipFilter is a bounded negative-lookup accelerator. A
// possible hit must still be verified against durable storage; saturation can
// therefore increase point reads but cannot admit a duplicate message.
type idempotencyMembershipFilter struct {
	// primary starts as hash pairs for sparse Channels. At the ninth distinct
	// key it becomes the usual fixed-size Bloom layer.
	primary      []uint64
	overflowBits []uint64
	primaryAdds  uint32
}

func (f *idempotencyMembershipFilter) mayContain(key []byte) bool {
	if f == nil || len(f.primary) == 0 {
		return false
	}
	h1, h2 := idempotencyMembershipHashes(key)
	return f.primaryMayContain(h1, h2) ||
		idempotencyMembershipLayerMayContain(f.overflowBits, h1, h2)
}

func (f *idempotencyMembershipFilter) add(key []byte) {
	if f == nil {
		return
	}
	h1, h2 := idempotencyMembershipHashes(key)
	if f.primaryMayContain(h1, h2) ||
		idempotencyMembershipLayerMayContain(f.overflowBits, h1, h2) {
		return
	}
	if f.primaryAdds < idempotencyMembershipSparseKeys {
		f.primary = append(f.primary, h1, h2)
		f.primaryAdds++
		return
	}
	if f.primaryAdds < idempotencyMembershipPrimaryCapacity {
		if len(f.primary) != idempotencyMembershipPrimaryWords {
			bits := make([]uint64, idempotencyMembershipPrimaryWords)
			for i := 0; i < len(f.primary); i += 2 {
				idempotencyMembershipLayerAdd(bits, f.primary[i], f.primary[i+1])
			}
			f.primary = bits
		}
		idempotencyMembershipLayerAdd(f.primary, h1, h2)
		f.primaryAdds++
		return
	}
	if f.overflowBits == nil {
		f.overflowBits = make([]uint64, idempotencyMembershipOverflowWords)
	}
	idempotencyMembershipLayerAdd(f.overflowBits, h1, h2)
}

func (f *idempotencyMembershipFilter) primaryMayContain(h1, h2 uint64) bool {
	if len(f.primary) == idempotencyMembershipPrimaryWords {
		return idempotencyMembershipLayerMayContain(f.primary, h1, h2)
	}
	for i := 0; i < len(f.primary); i += 2 {
		if f.primary[i] == h1 && f.primary[i+1] == h2 {
			return true
		}
	}
	return false
}

func idempotencyMembershipHashes(key []byte) (uint64, uint64) {
	h1 := maphash.Bytes(idempotencyMembershipSeed1, key)
	// An odd step visits the complete power-of-two bit range.
	h2 := maphash.Bytes(idempotencyMembershipSeed2, key) | 1
	return h1, h2
}

func idempotencyMembershipLayerMayContain(bits []uint64, h1 uint64, h2 uint64) bool {
	if len(bits) == 0 {
		return false
	}
	mask := uint64(len(bits)*64 - 1)
	for i := uint64(0); i < idempotencyMembershipHashCount; i++ {
		bit := (h1 + i*h2) & mask
		if bits[bit>>6]&(uint64(1)<<(bit&63)) == 0 {
			return false
		}
	}
	return true
}

func idempotencyMembershipLayerAdd(bits []uint64, h1 uint64, h2 uint64) {
	mask := uint64(len(bits)*64 - 1)
	for i := uint64(0); i < idempotencyMembershipHashCount; i++ {
		bit := (h1 + i*h2) & mask
		bits[bit>>6] |= uint64(1) << (bit & 63)
	}
}
