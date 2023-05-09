package wkutil

import (
	"crypto/md5"
	"encoding/hex"
)

// MD5 加密
func MD5(str string) string {
	h := md5.New()
	h.Write([]byte(str)) // 需要加密的字符串
	passwordmdsBys := h.Sum(nil)
	return hex.EncodeToString(passwordmdsBys)
}
