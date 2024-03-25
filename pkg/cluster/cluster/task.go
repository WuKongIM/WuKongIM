package cluster

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type taskQueue struct {
	mu                  sync.Mutex
	tasks               []task
	initialTaskQueueCap int
}

func newTaskQueue(initialTaskQueueCap int) *taskQueue {
	return &taskQueue{
		initialTaskQueueCap: initialTaskQueueCap,
		tasks:               make([]task, 0, initialTaskQueueCap),
	}
}

func (t *taskQueue) add(task task) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tasks = append(t.tasks, task)
}

func (t *taskQueue) getAll() []task {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := t.tasks
	t.tasks = make([]task, 0, t.initialTaskQueueCap)
	return result
}

func (t *taskQueue) get(taskKey string) task {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, task := range t.tasks {
		if task.taskKey() == taskKey {
			return task
		}
	}
	return nil
}

func (t *taskQueue) exists(taskKey string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, task := range t.tasks {
		if task.taskKey() == taskKey {
			return true
		}
	}
	return false
}

func (t *taskQueue) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.tasks)
}

func (t *taskQueue) empty() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.tasks) == 0
}

func (t *taskQueue) first() task {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.tasks) == 0 {
		return nil
	}
	return t.tasks[0]
}

func (t *taskQueue) removeFirst() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.tasks) == 0 {
		return
	}
	t.tasks = t.tasks[1:]
}

func (t *taskQueue) setFinished(taskKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, task := range t.tasks {
		if task.taskKey() == taskKey {
			task.taskFinished()
			return
		}
	}
}

func (t *taskQueue) remove(task task) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := 0; i < len(t.tasks); i++ {
		if t.tasks[i].taskKey() == task.taskKey() {
			t.tasks = append(t.tasks[:i], t.tasks[i+1:]...)
			return
		}
	}
}

type task interface {
	taskKey() string
	isTaskFinished() bool
	taskFinished()
}

type syncTask struct {
	startLogIndex uint64
	finished      bool
	key           string
	mu            sync.Mutex
	resp          replica.Message
}

func newSyncTask(nodeId uint64, startLogIndex uint64) *syncTask {
	return &syncTask{
		startLogIndex: startLogIndex,
		key:           getSyncTaskKey(nodeId, startLogIndex),
	}
}

func (s *syncTask) taskKey() string {
	return s.key
}

func (s *syncTask) setResp(resp replica.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resp = resp
}

func (s *syncTask) isTaskFinished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished
}

func (s *syncTask) taskFinished() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = true

}

func getSyncTaskKey(nodeId uint64, startLogIndex uint64) string {
	return fmt.Sprintf("sync:%d-%d", nodeId, startLogIndex)
}
