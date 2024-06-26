package pb

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlotSetMarshal(t *testing.T) {
	slots := make([]*Slot, 0)

	slots = append(slots, &Slot{
		Id:       1,
		Leader:   1,
		Term:     1,
		Replicas: []uint64{1, 2},
		Learners: []uint64{3, 4},
	})

	slots = append(slots, &Slot{
		Id:       2,
		Leader:   2,
		Term:     2,
		Replicas: []uint64{2, 3},
		Learners: []uint64{4, 5},
	})

	slotSet := SlotSet(slots)
	data, err := slotSet.Marshal()
	assert.Nil(t, err)

	var slotSet2 SlotSet
	err = slotSet2.Unmarshal(data)
	assert.Nil(t, err)

	assert.Equal(t, len(slotSet), len(slotSet2))

}
