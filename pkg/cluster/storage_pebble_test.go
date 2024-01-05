package cluster_test

import (
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/stretchr/testify/assert"
)

func TestStoragePebbleAppendLog(t *testing.T) {
	dataDir := path.Join(os.TempDir(), "pebble")
	fmt.Println("dataDir-->", dataDir)
	st := cluster.NewPebbleStorage(dataDir)
	err := st.Open()
	assert.NoError(t, err)
	defer st.Close()

	err = st.AppendLog("shardTest", replica.Log{
		Index: 1,
		Data:  []byte("hello1"),
	})
	assert.NoError(t, err)

	err = st.AppendLog("shardTest", replica.Log{
		Index: 2,
		Data:  []byte("hello2"),
	})
	assert.NoError(t, err)

	err = st.AppendLog("shardTest", replica.Log{
		Index: 3,
		Data:  []byte("hello3"),
	})
	assert.NoError(t, err)

	err = st.AppendLog("good", replica.Log{
		Index: 4,
		Data:  []byte("hello"),
	})
	assert.NoError(t, err)
	// test get logs 1
	logs, err := st.GetLogs("shardTest", 1, 3)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(logs))
	assert.Equal(t, uint64(1), logs[0].Index)
	assert.Equal(t, uint64(2), logs[1].Index)
	assert.Equal(t, uint64(3), logs[2].Index)
	assert.Equal(t, []byte("hello1"), logs[0].Data)
	assert.Equal(t, []byte("hello2"), logs[1].Data)
	assert.Equal(t, []byte("hello3"), logs[2].Data)

}
