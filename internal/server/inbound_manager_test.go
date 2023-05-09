package server

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring"
)

func TestTdd(t *testing.T) {
	bm := roaring.BitmapOf(1 << 31)

	v := bm.Contains(1 << 30)

	fmt.Println("v--->", v)

	v = bm.Contains(1 << 31)

	fmt.Println("v--->", v)
}
