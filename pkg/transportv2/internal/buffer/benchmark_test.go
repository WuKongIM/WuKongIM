package buffer

import "testing"

func BenchmarkSlabPoolGetRelease(b *testing.B) {
	cases := []struct {
		name string
		size int
	}{
		{name: "128B", size: 128},
		{name: "4KiB", size: 4 << 10},
		{name: "64KiB", size: 64 << 10},
		{name: "1MiB", size: 1 << 20},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			pool := NewSlabPool([]int{512, 4096, 65536, 1048576})

			b.SetBytes(int64(tc.size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := pool.Get(tc.size)
				if buf.Len() != tc.size {
					b.Fatalf("Len() = %d, want %d", buf.Len(), tc.size)
				}
				buf.Release()
			}
		})
	}
}

func BenchmarkSlabPoolParallelGetRelease(b *testing.B) {
	pool := NewSlabPool([]int{512, 4096, 65536, 1048576})
	const size = 4 << 10

	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := pool.Get(size)
			buf.Release()
		}
	})
}
