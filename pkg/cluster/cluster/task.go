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

	if task.needExec() {
		go func() { // TODO: 这里应该使用协程池来管理
			if err := task.exec(); err != nil {
				fmt.Println("task exec error:", err)
				task.setErr(err)
			}
			task.taskFinished()
		}()
	}
}

func (t *taskQueue) getAll() []task {
	t.mu.Lock()
	defer t.mu.Unlock()

	tasks := make([]task, len(t.tasks))
	copy(tasks, t.tasks)
	return tasks
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

func (t *taskQueue) remove(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := 0; i < len(t.tasks); i++ {
		if t.tasks[i].taskKey() == key {
			t.tasks = append(t.tasks[:i], t.tasks[i+1:]...)
			return
		}
	}
}

type task interface {
	taskKey() string
	isTaskFinished() bool
	taskFinished()
	needExec() bool
	setExec(f func() error)
	exec() error
	setResp(m replica.Message)
	resp() replica.Message
	setErr(err error)
	err() error
	hasErr() bool
}

type defaultTask struct {
	key      string
	finished bool
	mu       sync.RWMutex
	execFunc func() error
	rsp      replica.Message
	er       error
}

func newDefaultTask(key string) *defaultTask {
	return &defaultTask{
		key: key,
	}
}

func (d *defaultTask) taskKey() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.key
}

func (d *defaultTask) isTaskFinished() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.finished

}

func (d *defaultTask) taskFinished() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.finished = true
}

func (d *defaultTask) needExec() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.execFunc != nil
}

func (d *defaultTask) setExec(f func() error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execFunc = f
}

func (d *defaultTask) exec() error {
	return d.execFunc()
}

func (d *defaultTask) setResp(resp replica.Message) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rsp = resp
}

func (d *defaultTask) resp() replica.Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rsp
}

func (d *defaultTask) setErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.er = err
}

func (d *defaultTask) err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.er
}

func (d *defaultTask) hasErr() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.er != nil
}

type syncTask struct {
	defaultTask
}

func newSyncTask(nodeId uint64, startLogIndex uint64) *syncTask {
	return &syncTask{
		defaultTask: defaultTask{
			key: getSyncTaskKey(nodeId, startLogIndex),
		},
	}
}

func getSyncTaskKey(nodeId uint64, startLogIndex uint64) string {
	return fmt.Sprintf("sync:%d-%d", nodeId, startLogIndex)
}

type getLogsTask struct {
	*defaultTask
}

func newGetLogsTask(startIndex uint64) *getLogsTask {
	return &getLogsTask{
		defaultTask: newDefaultTask(getGetLogsTaskTaskKey(startIndex)),
	}
}

type storeAppendTask struct {
	*defaultTask
	endLogIndex uint64
}

func newStoreAppendTask(endLogIndex uint64) *storeAppendTask {
	return &storeAppendTask{
		defaultTask: newDefaultTask(getStoreAppendTaskKey(endLogIndex)),
		endLogIndex: endLogIndex,
	}
}

type applyLogsTask struct {
	*defaultTask
	committedIndex uint64
}

func newApplyLogsTask(committedIndex uint64) *applyLogsTask {
	return &applyLogsTask{
		defaultTask:    newDefaultTask(fmt.Sprintf("applyLogs:%d", committedIndex)),
		committedIndex: committedIndex,
	}
}

func getGetLogsTaskTaskKey(startLogIndex uint64) string {
	return fmt.Sprintf("getLogs:%d", startLogIndex)
}

func getStoreAppendTaskKey(endLogIndex uint64) string {
	return fmt.Sprintf("storeAppend:%d", endLogIndex)
}
