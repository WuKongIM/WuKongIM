package reactor

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMessageWait(t *testing.T) {
	messageIds := []uint64{1, 2, 3}
	m := newProposeWait()
	key := "test"
	waitC := m.add(context.Background(), key, messageIds)

	select {
	case <-waitC:
		t.Fatal("should not close")
	default:
	}

	m.didPropose(key, 1, 1)
	m.didPropose(key, 2, 2)
	m.didPropose(key, 3, 3)

	m.didCommit(1, 2)

	select {
	case <-waitC:
		t.Fatal("should not close")
	default:
	}

	m.didCommit(2, 4)

	select {
	case items := <-waitC:
		assert.Equal(t, 3, len(items))
	default:
		t.Fatal("should close")
	}
}

func BenchmarkMessageWait(b *testing.B) {
	messageIds := make([]uint64, 0)
	m := newProposeWait()

	b.StartTimer()
	num := b.N
	for i := 1; i <= num; i++ {
		messageIds = append(messageIds, uint64(i))
	}
	key := strconv.FormatUint(messageIds[len(messageIds)-1], 10)

	fmt.Println("num", num)

	_ = m.add(context.Background(), key, messageIds)

	start := time.Now()
	for i := 1; i <= num; i++ {
		m.didPropose(key, uint64(i), uint64(i))
	}
	fmt.Println("didPropose time", time.Since(start))

	start = time.Now()
	m.didCommit(1, uint64(num)+1)
	fmt.Println("didCommit time", time.Since(start))

	// select {
	// case <-waitC:
	// default:
	// 	b.Fatal("should close")
	// }

}
