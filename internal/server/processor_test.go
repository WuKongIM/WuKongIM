package server

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/stretchr/testify/assert"
)

func TestSameFrames(t *testing.T) {
	/*
		{"level":"error","time":"2023-05-18 10:22:03","msg":"【Processor】msgKey is illegal！","except":"c7e576e204c36a7e5790b212918f48e2","act":"bcb93b08aad683e26d3f4c7e7a0c24b0","sign":"725f39572fc667422f90711d92158a605ach0016npVd/QB90ea7JK7mt907BtEPynnK2tDfk29SqQ0qeX71TBTm2gjK5zYUqyAmCp+DNNdkW6smnlym1NCTIB5yny8O/PbetlBmcPTR7CtmnD4V0vVlEx2N9hwfMdHIHeKIsnYZFogmCbfzP2u1pNI7S4uHxB4h4EiiUwsFLVaXTp0GJnZeFffC5DwxmIaeSRpN","aesKey":"0a629cbb39ec7440","aesIV":"zICaz2hQYvO0SyWr","conn":"Conn[22] uid=1882caa11a8-21-21021"}
	*/
	signStr := "725f39572fc667422f90711d92158a605ach0016npVd/QB90ea7JK7mt907BtEPynnK2tDfk29SqQ0qeX71TBTm2gjK5zYUqyAmCp+DNNdkW6smnlym1NCTIB5yny8O/PbetlBmcPTR7CtmnD4V0vVlEx2N9hwfMdHIHeKIsnYZFogmCbfzP2u1pNI7S4uHxB4h4EiiUwsFLVaXTp0GJnZeFffC5DwxmIaeSRpN"
	aesKey := "0a629cbb39ec7440"
	aesIV := "zICaz2hQYvO0SyWr"
	actMsgKey, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	assert.NoError(t, err)
	assert.Equal(t, "bcb93b08aad683e26d3f4c7e7a0c24b0", string(wkutil.MD5(string(actMsgKey))))
}
