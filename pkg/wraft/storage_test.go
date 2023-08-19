// Copyright (c) 2022 Shanghai Xinbida Network Technology Co., Ltd. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package wraft_test

import (
	"os"
	"path"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wraft"
	"github.com/stretchr/testify/assert"
	pb "go.etcd.io/raft/v3/raftpb"
)

func testWalStorage(dir string) *wraft.WALStorage {

	return wraft.NewWALStorage(path.Join(dir, "wal"), path.Join(dir, "meta.db"))
}

func TestHardState(t *testing.T) {
	tmpDir := path.Join(os.TempDir(), "storage")
	defer os.RemoveAll(tmpDir)

	storage := testWalStorage(tmpDir)

	hardState, _, err := storage.InitialState()
	assert.NoError(t, err)

	assert.Equal(t, uint64(0), hardState.Term)
	assert.Equal(t, uint64(0), hardState.Vote)
	assert.Equal(t, uint64(0), hardState.Commit)

	err = storage.SetHardState(pb.HardState{
		Term:   1,
		Vote:   2,
		Commit: 3,
	})
	assert.NoError(t, err)

	hardState, _, err = storage.InitialState()
	assert.NoError(t, err)

	assert.Equal(t, uint64(1), hardState.Term)
	assert.Equal(t, uint64(2), hardState.Vote)
	assert.Equal(t, uint64(3), hardState.Commit)
}

func TestEntries(t *testing.T) {
	tmpDir := path.Join(os.TempDir(), "storage")
	defer os.RemoveAll(tmpDir)
	storage := testWalStorage(tmpDir)

	err := storage.Append([]pb.Entry{
		{
			Index: 1,
			Term:  1,
			Type:  pb.EntryNormal,
			Data:  []byte("hello"),
		},
	})
	assert.NoError(t, err)

	entries, err := storage.Entries(1, 2, 1000)
	assert.NoError(t, err)

	assert.Equal(t, 1, len(entries))
	assert.Equal(t, uint64(1), entries[0].Index)
	assert.Equal(t, uint64(1), entries[0].Term)
	assert.Equal(t, pb.EntryNormal, entries[0].Type)
	assert.Equal(t, []byte("hello"), entries[0].Data)

}

func TestTerm(t *testing.T) {
	tmpDir := path.Join(os.TempDir(), "storage")
	defer os.RemoveAll(tmpDir)
	storage := testWalStorage(tmpDir)

	err := storage.Append([]pb.Entry{
		{
			Index: 1,
			Term:  2,
			Type:  pb.EntryNormal,
			Data:  []byte("hello"),
		},
	})
	assert.NoError(t, err)

	term, err := storage.Term(1)
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), term)
}

func TestFirstAndLastIndex(t *testing.T) {
	tmpDir := path.Join(os.TempDir(), "storage")
	defer os.RemoveAll(tmpDir)
	storage := testWalStorage(tmpDir)
	err := storage.Append([]pb.Entry{
		{
			Index: 1,
			Term:  1,
			Type:  pb.EntryNormal,
			Data:  []byte("hello1"),
		},
		{
			Index: 2,
			Term:  2,
			Type:  pb.EntryNormal,
			Data:  []byte("hello2"),
		},
	})
	assert.NoError(t, err)

	firstIndex, err := storage.FirstIndex()
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), firstIndex)

	lastIndex, err := storage.LastIndex()
	assert.NoError(t, err)
	assert.Equal(t, uint64(2), lastIndex)

}
