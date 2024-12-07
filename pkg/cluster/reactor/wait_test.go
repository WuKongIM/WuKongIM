package reactor

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageWait(t *testing.T) {
	messageIds := []uint64{1, 2, 3}
	m := newProposeWait("test")
	key := "test"
	progress := m.add(key, messageIds[0], messageIds[len(messageIds)-1])

	select {
	case <-progress.waitC:
		t.Fatal("should not close")
	default:
	}

	m.didPropose(key, 1, 3, 1)

	m.didCommit(1, 2)

	select {
	case <-progress.waitC:
		t.Fatal("should not close")
	default:
	}

	m.didCommit(2, 4)

	select {
	case err := <-progress.waitC:
		assert.Nil(t, err)
		// assert.Equal(t, 3, len(items))
	default:
		t.Fatal("should close")
	}
}

func BenchmarkMessageWait(b *testing.B) {
	messageIds := make([]uint64, 0)
	m := newProposeWait("test")

	b.StartTimer()
	num := b.N
	for i := 1; i <= num; i++ {
		messageIds = append(messageIds, uint64(i))
	}
	key := strconv.FormatUint(messageIds[len(messageIds)-1], 10)

	_ = m.add(key, messageIds[0], messageIds[len(messageIds)-1])

	for i := 1; i <= num; i++ {
		m.didPropose(key, uint64(i), uint64(i), 1)
	}

	m.didCommit(1, uint64(num)+1)

	// select {
	// case <-waitC:
	// default:
	// 	b.Fatal("should close")
	// }

}

// func TestAnts(t *testing.T) {
// 	antsPool, err := ants.NewMultiPool(10, 10, ants.LeastTasks)
// 	assert.Nil(t, err)

// 	reqs := make([]*testReq, 0)

// 	for i := 0; i < 1000; i++ {
// 		reqs = append(reqs, &testReq{no: strconv.Itoa(i)})
// 	}

// 	for _, req := range reqs {
// 		err := antsPool.Submit(func() {
// 			t.Log(req.no)
// 		})
// 		assert.Nil(t, err)
// 	}
// }

// type testReq struct {
// 	no string
// }
