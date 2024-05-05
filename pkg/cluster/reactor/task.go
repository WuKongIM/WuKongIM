package reactor

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type taskQueue struct {
	mu                  sync.RWMutex
	tasks               []task
	initialTaskQueueCap int
	r                   *Reactor
	wklog.Log
}

func newTaskQueue(r *Reactor, initialTaskQueueCap int) *taskQueue {
	return &taskQueue{
		initialTaskQueueCap: initialTaskQueueCap,
		tasks:               make([]task, 0, initialTaskQueueCap),
		r:                   r,
		Log:                 wklog.NewWKLog("taskQueue"),
	}
}

func (t *taskQueue) add(task task) {
	t.mu.Lock()
	t.tasks = append(t.tasks, task)
	t.mu.Unlock()

	if task.needExec() {
		err := t.r.submitTask(func() {
			if err := task.exec(); err != nil {
				fmt.Println("task exec error:", err)
				task.setErr(err)
			}
			task.taskFinished()
		})
		if err != nil {
			t.Error("submit task error", zap.String("taskKey", task.taskKey()), zap.Error(err))
		}
	}
}

func (t *taskQueue) getAll() []task {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.tasks[0:]
}

func (t *taskQueue) get(taskKey string) task {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, task := range t.tasks {
		if task.taskKey() == taskKey {
			return task
		}
	}
	return nil
}

func (t *taskQueue) exists(taskKey string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, task := range t.tasks {
		if task.taskKey() == taskKey {
			return true
		}
	}
	return false
}

func (t *taskQueue) len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.tasks)
}

func (t *taskQueue) empty() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.tasks) == 0
}

func (t *taskQueue) first() task {
	t.mu.RLock()
	defer t.mu.RUnlock()
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
	var b strings.Builder
	b.WriteString("sync:")
	b.WriteString(strconv.FormatUint(nodeId, 10))
	b.WriteString("-")
	b.WriteString(strconv.FormatUint(startLogIndex, 10))
	return b.String()
}

type getLogsTask struct {
	*defaultTask
}

func newGetLogsTask(nodeId uint64, startIndex uint64) *getLogsTask {
	return &getLogsTask{
		defaultTask: newDefaultTask(getGetLogsTaskTaskKey(nodeId, startIndex)),
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

func getGetLogsTaskTaskKey(nodeId uint64, startLogIndex uint64) string {
	var b strings.Builder
	b.WriteString("getLogs:")
	b.WriteString(strconv.FormatUint(nodeId, 10))
	b.WriteString("-")
	b.WriteString(strconv.FormatUint(startLogIndex, 10))
	return b.String()
}

func getStoreAppendTaskKey(endLogIndex uint64) string {
	return fmt.Sprintf("storeAppend:%d", endLogIndex)
}
