package wkstore

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileStoreMsg(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)
	store := NewFileStore(&StoreConfig{
		SlotNum: 1024,
		DataDir: dir,
	})

	store.AppendMessages("testtopic", 1, []Message{
		&testMessage{
			data: []byte("test1"),
		},
		&testMessage{
			data: []byte("test2"),
		},
	})
}

func TestFileWriteAndRead(t *testing.T) {

	f1, _ := os.OpenFile("test.txt", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0755)

	defer f1.Close()

	f1.Write([]byte("test1"))

	f2, _ := os.OpenFile("test.txt", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0755)

	testBytes := make([]byte, 100)
	n, _ := f2.ReadAt(testBytes, 0)
	fmt.Println("zzz--->", string(testBytes[:n]))

}
