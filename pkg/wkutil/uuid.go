package wkutil

import (
	"strings"

	"github.com/google/uuid"
)

// GenUUID 生成uuid
func GenUUID() string {
	uuid.EnableRandPool()
	u1 := uuid.New()
	return strings.Replace(u1.String(), "-", "", -1)
}
