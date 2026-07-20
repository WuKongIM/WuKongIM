package conversationactive

// dirtyAgeBucket counts live dirty rows sharing one positive ActiveAtMS value.
type dirtyAgeBucket struct {
	activeAtMS int64
	count      int
}

// dirtyAgeIndex is a bounded, deletable min-heap keyed by live dirty ActiveAtMS values.
// Callers must hold Manager.mu while using it.
type dirtyAgeIndex struct {
	heap      []dirtyAgeBucket
	positions map[int64]int
}

func (i *dirtyAgeIndex) Add(activeAtMS int64) {
	if activeAtMS <= 0 {
		return
	}
	if position, ok := i.positions[activeAtMS]; ok {
		i.heap[position].count++
		return
	}
	if i.positions == nil {
		i.positions = make(map[int64]int)
	}
	position := len(i.heap)
	i.heap = append(i.heap, dirtyAgeBucket{activeAtMS: activeAtMS, count: 1})
	i.positions[activeAtMS] = position
	i.siftUp(position)
}

func (i *dirtyAgeIndex) Remove(activeAtMS int64) {
	if activeAtMS <= 0 || len(i.positions) == 0 {
		return
	}
	position, ok := i.positions[activeAtMS]
	if !ok {
		return
	}
	if i.heap[position].count > 1 {
		i.heap[position].count--
		return
	}
	i.removeAt(position)
}

// Move transfers one live dirty row between timestamps without growing the heap.
func (i *dirtyAgeIndex) Move(oldActiveAtMS, newActiveAtMS int64) {
	if oldActiveAtMS == newActiveAtMS {
		return
	}
	if oldActiveAtMS <= 0 {
		i.Add(newActiveAtMS)
		return
	}
	if newActiveAtMS <= 0 {
		i.Remove(oldActiveAtMS)
		return
	}
	oldPosition, oldExists := i.positions[oldActiveAtMS]
	_, newExists := i.positions[newActiveAtMS]
	if !oldExists || i.heap[oldPosition].count != 1 || newExists {
		i.Remove(oldActiveAtMS)
		i.Add(newActiveAtMS)
		return
	}

	delete(i.positions, oldActiveAtMS)
	i.heap[oldPosition].activeAtMS = newActiveAtMS
	i.positions[newActiveAtMS] = oldPosition
	if newActiveAtMS < oldActiveAtMS {
		i.siftUp(oldPosition)
		return
	}
	i.siftDown(oldPosition)
}

func (i *dirtyAgeIndex) Oldest() int64 {
	if len(i.heap) == 0 {
		return 0
	}
	return i.heap[0].activeAtMS
}

func (i *dirtyAgeIndex) Len() int {
	return len(i.heap)
}

func (i *dirtyAgeIndex) removeAt(position int) {
	last := len(i.heap) - 1
	removed := i.heap[position].activeAtMS
	delete(i.positions, removed)
	if position == last {
		i.heap = i.heap[:last]
		return
	}

	moved := i.heap[last]
	i.heap[position] = moved
	i.heap = i.heap[:last]
	i.positions[moved.activeAtMS] = position
	if position > 0 && i.heap[position].activeAtMS < i.heap[(position-1)/2].activeAtMS {
		i.siftUp(position)
		return
	}
	i.siftDown(position)
}

func (i *dirtyAgeIndex) siftUp(position int) {
	for position > 0 {
		parent := (position - 1) / 2
		if i.heap[parent].activeAtMS <= i.heap[position].activeAtMS {
			return
		}
		i.swap(parent, position)
		position = parent
	}
}

func (i *dirtyAgeIndex) siftDown(position int) {
	for {
		left := position*2 + 1
		if left >= len(i.heap) {
			return
		}
		smallest := left
		right := left + 1
		if right < len(i.heap) && i.heap[right].activeAtMS < i.heap[left].activeAtMS {
			smallest = right
		}
		if i.heap[position].activeAtMS <= i.heap[smallest].activeAtMS {
			return
		}
		i.swap(position, smallest)
		position = smallest
	}
}

func (i *dirtyAgeIndex) swap(left, right int) {
	i.heap[left], i.heap[right] = i.heap[right], i.heap[left]
	i.positions[i.heap[left].activeAtMS] = left
	i.positions[i.heap[right].activeAtMS] = right
}
