package buffer

import "testing"

func TestDefaultSlabsBoundRoundingOverhead(t *testing.T) {
	for n := 512; n <= 1<<20; n++ {
		for _, class := range DefaultSlabPool.classes {
			if class.size < n {
				continue
			}
			if class.size > 2*n {
				t.Fatalf("payload %d uses class %d, exceeding 2x", n, class.size)
			}
			break
		}
	}
}

func TestSlabCostSurvivesPayloadCapacityLimit(t *testing.T) {
	for _, n := range []int{513, 4097, 65537} {
		buf := DefaultSlabPool.Get(n)
		if cap(buf.Bytes()) != n || buf.RetainedBytes() < n || buf.RetainedBytes() > 2*n {
			t.Fatalf("n=%d cap=%d retained=%d", n, cap(buf.Bytes()), buf.RetainedBytes())
		}
		buf.Release()
		if buf.RetainedBytes() != 0 {
			t.Fatal("released buffer retains an admission charge")
		}
	}
}
