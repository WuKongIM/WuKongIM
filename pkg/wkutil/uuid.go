package wkutil

import (
	"strings"

	"github.com/google/uuid"
)

func init() {
	uuid.EnableRandPool()
}

// GenUUID 生成uuid
func GenUUID() string {
	u1 := uuid.New()
	return strings.Replace(u1.String(), "-", "", -1)
}
