package wkutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/bytebufferpool"
)

func TestAesEncryptPkcs7Base64ForPool(t *testing.T) {
	resultBuff := bytebufferpool.Get()
	defer bytebufferpool.Put(resultBuff)
	err := AesEncryptPkcs7Base64ForPool([]byte("test"), []byte("1234567890123456"), []byte("1234567890123456"), resultBuff)
	assert.NoError(t, err)

	assert.NotEmpty(t, resultBuff.B)

}
