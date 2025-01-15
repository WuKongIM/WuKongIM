package event

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type EventQueue struct {
	events           []*types.Event
	offsetEventIndex uint64
	wklog.Log
	lastIndex uint64 // 最新下标
}

func NewEventQueue(prefix string) *EventQueue {
	return &EventQueue{
		Log:              wklog.NewWKLog(fmt.Sprintf("EventQueue[%s]", prefix)),
		offsetEventIndex: 1, // 消息下标是从1开始的 所以offset初始化值为1
	}
}

func (m *EventQueue) Append(event *types.Event) {
	m.events = append(m.events, event)
	m.lastIndex++
}

func (m *EventQueue) Len() int {
	return len(m.events)
}

// [lo,hi)
func (m *EventQueue) Slice(startEventIndex uint64, endEventIndex uint64) []*types.Event {

	return m.events[startEventIndex-m.offsetEventIndex : endEventIndex-m.offsetEventIndex : endEventIndex-m.offsetEventIndex]
}

// truncateTo 裁剪eventIndex之前的消息,不包含eventIndex
func (m *EventQueue) TruncateTo(eventIndex uint64) {
	num := m.getArrayIndex(eventIndex)
	m.events = m.events[num:]
	m.offsetEventIndex = eventIndex
	m.shrinkEventsArray()
}

func (m *EventQueue) LastIndex() uint64 {
	return m.lastIndex
}

func (m *EventQueue) getArrayIndex(eventIndex uint64) int {

	return int(eventIndex - m.offsetEventIndex)
}

func (m *EventQueue) reset() {
	m.events = m.events[:0]
	m.offsetEventIndex = 1
	m.lastIndex = 0
}

func (m *EventQueue) shrinkEventsArray() {
	const lenMultiple = 2
	if len(m.events) == 0 {
		m.events = nil
	} else if len(m.events)*lenMultiple < cap(m.events) {
		newEvents := make([]*types.Event, len(m.events))
		copy(newEvents, m.events)
		m.events = newEvents
	}
}
