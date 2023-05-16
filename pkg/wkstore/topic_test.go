package wkstore

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTopicAppend(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	defer os.Remove(dir)
	assert.NoError(t, err)
	cfg := NewStoreConfig()
	cfg.DataDir = dir
	tc := newTopic("test", 1, cfg)

	seqs, err := tc.appendMessages([]Message{
		&testMessage{
			data: []byte("hello1"),
		},
		&testMessage{
			data: []byte("hello2"),
		},
		&testMessage{
			data: []byte("hello3"),
		},
	})
	assert.NoError(t, err)

	assert.Equal(t, []uint32{1, 2, 3}, seqs)

}

func BenchmarkTopicAppend(b *testing.B) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(b, err)
	// fmt.Println("dir-->", dir)
	defer os.Remove(dir)
	assert.NoError(b, err)
	cfg := NewStoreConfig()
	cfg.DataDir = dir
	tc := newTopic("test", 1, cfg)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := tc.appendMessages([]Message{
			&testMessage{
				data: []byte("hello"),
			},
		})
		assert.NoError(b, err)
	}
}

func BenchmarkCommFileWrite(b *testing.B) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(b, err)
	defer os.Remove(dir)

	f, err := os.OpenFile(filepath.Join(dir, "test"), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0755)
	assert.NoError(b, err)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		f.Write([]byte("hellohellohellohellohellohellohellohellohellohellohellohellohellohellohellohellohellohellohellohellohellohello"))
	}
}
