package conversationactive

import "testing"

func TestDirtyAddressQueueRotatesAndRemovesWithoutDuplicates(t *testing.T) {
	addresses := []cacheAddress{
		{uid: "u1", key: conversationKey{channelID: "room"}},
		{uid: "u2", key: conversationKey{channelID: "room"}},
		{uid: "u3", key: conversationKey{channelID: "room"}},
	}
	var queue dirtyAddressQueue
	for _, address := range addresses {
		queue.Add(address)
		queue.Add(address)
	}
	assertDirtyAddressQueueInvariant(t, &queue)
	if got := queue.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}

	queue.MoveToBack(addresses[0])
	assertDirtyAddressQueueOrder(t, &queue, []cacheAddress{addresses[1], addresses[2], addresses[0]})
	queue.MoveToBack(addresses[0])
	queue.MoveToBack(cacheAddress{uid: "missing"})
	assertDirtyAddressQueueOrder(t, &queue, []cacheAddress{addresses[1], addresses[2], addresses[0]})
	queue.Remove(addresses[2])
	assertDirtyAddressQueueOrder(t, &queue, []cacheAddress{addresses[1], addresses[0]})
	queue.Remove(addresses[1])
	queue.Remove(addresses[0])
	assertDirtyAddressQueueOrder(t, &queue, nil)
	queue.Add(addresses[2])
	assertDirtyAddressQueueOrder(t, &queue, []cacheAddress{addresses[2]})
}

func assertDirtyAddressQueueOrder(t *testing.T, queue *dirtyAddressQueue, want []cacheAddress) {
	t.Helper()
	assertDirtyAddressQueueInvariant(t, queue)
	got := make([]cacheAddress, 0, queue.Len())
	address, ok := queue.Front()
	for ok {
		got = append(got, address)
		address, ok = queue.Next(address)
	}
	if len(got) != len(want) {
		t.Fatalf("queue order len=%d, want %d; got=%v want=%v", len(got), len(want), got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("queue order[%d]=%v, want %v", index, got[index], want[index])
		}
	}
}

func assertDirtyAddressQueueInvariant(t *testing.T, queue *dirtyAddressQueue) {
	t.Helper()
	if queue.Len() == 0 {
		if queue.hasHead || queue.hasTail {
			t.Fatalf("empty queue retains head/tail flags: %+v", queue)
		}
		return
	}
	if !queue.hasHead || !queue.hasTail {
		t.Fatalf("non-empty queue is missing head/tail: %+v", queue)
	}
	seen := make(map[cacheAddress]struct{}, queue.Len())
	address := queue.head
	for {
		if _, duplicate := seen[address]; duplicate {
			t.Fatalf("queue cycle or duplicate at %v", address)
		}
		seen[address] = struct{}{}
		node, ok := queue.nodes[address]
		if !ok {
			t.Fatalf("queue address %v missing node", address)
		}
		if !node.hasNext {
			if address != queue.tail {
				t.Fatalf("terminal address %v, want tail %v", address, queue.tail)
			}
			break
		}
		next := queue.nodes[node.next]
		if !next.hasPrev || next.prev != address {
			t.Fatalf("broken reverse link %v -> %v", address, node.next)
		}
		address = node.next
	}
	if len(seen) != queue.Len() {
		t.Fatalf("reachable nodes=%d, queue len=%d", len(seen), queue.Len())
	}
}
