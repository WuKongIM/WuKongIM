package gateway

import "errors"

const (
	aesKeyKey = "aesKey"
	aesIVKey  = "aesIV"
)

var (
	ErrDataNotEnough = errors.New("data not enough")
)
