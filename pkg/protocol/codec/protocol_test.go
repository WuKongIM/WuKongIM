package codec

import (
	"fmt"
	"testing"
)

func TestEncodeAndDecodeLength(t *testing.T) {
	bys := encodeVariable(1241)
	fmt.Println(bys)
}
