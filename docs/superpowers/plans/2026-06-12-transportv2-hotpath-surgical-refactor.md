# transportv2 高并发热路径外科手术重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不改变公共 API 行为的前提下，分多个独立可验证的小 PR 削减 transportv2 高并发热路径的 CPU、堆分配与锁争用。

**Architecture:** 五个顺序 PR，逐层收敛热路径开销：① 调度器观测去 O(n²)；② 写路径每批缓冲复用；③ RPC 响应去拷贝 / 去 per-request goroutine；④ 读路径与 OwnedBuffer 分配优化；⑤ 调度器锁模型。每个 PR 各自带 benchmark 前后对比、全程 `-race`、不破坏既有观测语义。

**Tech Stack:** Go、`net.Buffers`/writev、`sync.Pool`(slab)、`sync.Cond`(调度器)、`panjf2000/ants`(executor)、Go testing/benchmark。

**关联 spec:** `docs/superpowers/specs/2026-06-12-transportv2-hotpath-surgical-refactor-design.md`

**全局约束（每个 PR 都适用）：**
- 工作目录 `pkg/transportv2`，所有测试命令默认在此根下执行。
- 不丢任何被 `internalv2/app/observability.go::transportV2MetricsObserver` 消费的指标语义。
- 任何涉及 `OwnedBuffer` 所有权的改动，验证命令必须带 `-race`。
- 提交信息结尾追加 `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`。

**前置说明（读测试后修正）：** `scheduler_wait` 事件**必须保留 `Bytes` 字段**（`scheduler_test.go:102` 断言它），
且 `waitEventLocked` 已是 O(1)，不动。PR1 真正的 O(n) 在 `snapshotLaneQueueLocked`（遍历整条 lane 累加字节），
它被 `nextBatchLocked` 每出队一帧经 `queueEventLocked` 调用一次，构成 O(B·N)。
故 PR1 = 给 lane 加 O(1) 计数器 + 把 `nextBatchLocked` 的 per-item 队列事件去重为 per-touched-priority。

---

## PR 1 — 调度器观测去 O(n²)

**文件：**
- 修改：`internal/sched/scheduler.go`
- 测试：`internal/sched/scheduler_test.go`、`internal/sched/benchmark_test.go`

### Task 1.1：给 lane 增加 O(1) 计数器并维护一致性

**Files:**
- Modify: `internal/sched/scheduler.go`（`lane` 结构体 46-51 行；`Enqueue` 157-169 行；`nextBatchLocked` 257-261 行；`drainLocked` 481-497 行）
- Test: `internal/sched/scheduler_test.go`

- [ ] **Step 1: 写失败测试 — lane 计数器与权威队列一致**

在 `scheduler_test.go` 末尾追加。该测试反复 enqueue/取批，断言每条 lane 的 `items/bytes`
计数器与真实 `len(queue)` 和逐项 `queueBytes` 之和一致（同包可访问私有字段）。

```go
func TestLaneCountersStayConsistent(t *testing.T) {
	s := New(Config{
		MaxItems:       64,
		MaxBytes:       4096,
		MaxBatchFrames: 4,
		MaxBatchBytes:  256,
	})
	priorities := []core.Priority{
		core.PriorityRaft, core.PriorityControl, core.PriorityRPC, core.PriorityBulk,
	}
	for round := 0; round < 50; round++ {
		for i, p := range priorities {
			_ = s.Enqueue(context.Background(), Item{Priority: p, Bytes: (i + 1) * 7, Value: round})
		}
		_ = s.NextBatch()
		s.mu.Lock()
		for li := range s.lanes {
			l := &s.lanes[li]
			wantItems := len(l.queue)
			var wantBytes int64
			for _, it := range l.queue {
				wantBytes += queueBytes(it)
			}
			if l.items != wantItems {
				s.mu.Unlock()
				t.Fatalf("lane %d items = %d, want %d", li, l.items, wantItems)
			}
			if l.bytes != wantBytes {
				s.mu.Unlock()
				t.Fatalf("lane %d bytes = %d, want %d", li, l.bytes, wantBytes)
			}
		}
		s.mu.Unlock()
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/sched/ -run TestLaneCountersStayConsistent -v`
Expected: 编译失败 — `l.items`/`l.bytes` 字段不存在。

- [ ] **Step 3: 实现 lane 计数器**

修改 `lane` 结构体（46-51 行）：

```go
type lane struct {
	priority core.Priority
	weight   int64
	deficit  int64
	queue    []Item
	items    int
	bytes    int64
}
```

在 `Enqueue` 入队成功分支（157-169 行 for 循环内）追加：

```go
	for i := range s.lanes {
		if s.lanes[i].priority == item.Priority {
			item.enqueuedAt = time.Now()
			s.lanes[i].queue = append(s.lanes[i].queue, item)
			s.lanes[i].items++
			s.lanes[i].bytes += queueBytes(item)
			s.queuedItems++
			s.queuedBytes += int64(item.Bytes)
			snapshot := s.snapshotQueueLocked()
			laneSnapshot := s.snapshotLaneQueueLocked(item.Priority)
			s.cond.Signal()
			s.mu.Unlock()
			s.observeEnqueue("ok", item, snapshot, laneSnapshot)
			return nil
		}
	}
```

在 `nextBatchLocked` 出队处（257-261 行）追加 lane 计数器递减：

```go
		l.queue[0] = Item{}
		l.queue = l.queue[1:]
		l.items--
		l.bytes -= itemBytes
		l.deficit -= itemCost
		s.queuedItems--
		s.queuedBytes -= itemBytes
```

在 `drainLocked`（481-497 行）重置 lane 计数器：

```go
	for i := range s.lanes {
		drained = append(drained, s.lanes[i].queue...)
		for j := range s.lanes[i].queue {
			s.lanes[i].queue[j] = Item{}
		}
		s.lanes[i].queue = nil
		s.lanes[i].deficit = 0
		s.lanes[i].items = 0
		s.lanes[i].bytes = 0
	}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/sched/ -run TestLaneCountersStayConsistent -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/sched/scheduler.go internal/sched/scheduler_test.go
git commit -m "perf(transportv2/sched): track per-lane item and byte counters

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

### Task 1.2：把 snapshotLaneQueueLocked 改为 O(1)

**Files:**
- Modify: `internal/sched/scheduler.go:378-394`
- Test: 复用既有 `internal/sched/scheduler_test.go`

- [ ] **Step 1: 实现 O(1) 查表**

将 `snapshotLaneQueueLocked`（378-394 行）改为直接读计数器，去掉对 `l.queue` 的遍历：

```go
func (s *Scheduler) snapshotLaneQueueLocked(priority core.Priority) queueSnapshot {
	snapshot := queueSnapshot{
		capacity:      s.maxItems,
		bytesCapacity: s.maxBytes,
	}
	for i := range s.lanes {
		if s.lanes[i].priority != priority {
			continue
		}
		snapshot.items = s.lanes[i].items
		snapshot.bytes = s.lanes[i].bytes
		return snapshot
	}
	return snapshot
}
```

- [ ] **Step 2: 运行 sched 全部测试确认通过**

Run: `go test ./internal/sched/ -race -v`
Expected: PASS（既有观测断言依赖 lane 深度，计数器与遍历结果等价）。

- [ ] **Step 3: 提交**

```bash
git add internal/sched/scheduler.go
git commit -m "perf(transportv2/sched): O(1) lane queue snapshot via counters

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

### Task 1.3：把 nextBatchLocked 的 per-item 队列事件去重为 per-touched-priority

**Files:**
- Modify: `internal/sched/scheduler.go:232-277`
- Test: 复用既有 `internal/sched/scheduler_test.go`

- [ ] **Step 1: 重写 nextBatchLocked 的事件生成**

仍 per-item 追加 `scheduler_wait`（O(1)、测试断言其 `Bytes`），但 `scheduler_queue` 改为批结束后对每个被触及 priority 各生成一个最终状态事件。用本地 `touched` 标记被出队的 lane：

```go
func (s *Scheduler) nextBatchLocked() ([]Item, []core.Event) {
	var batch []Item
	var events []core.Event
	var batchBytes int64
	touched := make([]bool, len(s.lanes))

	for len(batch) < s.maxBatchFrames && s.queuedItems > 0 {
		s.openRoundLocked()

		l := &s.lanes[s.nextLane]
		if len(l.queue) == 0 {
			s.finishLaneLocked()
			continue
		}

		item := l.queue[0]
		itemCost := scheduleCost(item)
		itemBytes := queueBytes(item)
		if itemCost > l.deficit {
			s.finishLaneLocked()
			continue
		}
		if batchBytes+itemBytes > s.maxBatchBytes {
			break
		}

		l.queue[0] = Item{}
		l.queue = l.queue[1:]
		l.items--
		l.bytes -= itemBytes
		l.deficit -= itemCost
		s.queuedItems--
		s.queuedBytes -= itemBytes
		batch = append(batch, item)
		events = append(events, s.waitEventLocked(item, itemBytes))
		touched[s.nextLane] = true
		batchBytes += itemBytes
		s.roundOutput = true

		if len(batch) >= s.maxBatchFrames || batchBytes >= s.maxBatchBytes {
			break
		}
		if len(l.queue) == 0 {
			s.finishLaneLocked()
		}
	}

	for i := range s.lanes {
		if touched[i] {
			events = append(events, s.queueEventLocked(s.lanes[i].priority, "ok"))
		}
	}

	return batch, events
}
```

- [ ] **Step 2: 运行 sched 全部测试确认通过**

Run: `go test ./internal/sched/ -race -v`
Expected: PASS。重点确认 `TestSchedulerObservesQueueAdmissionAndWait`（drained RPC 队列事件 Items:0）、
`TestSchedulerQueueEventsUsePriorityLaneDepth`（RPC lane drained Items:0、Bulk lane 仍 Items:1）通过 ——
被触及的 RPC lane 产生最终 Items:0 事件，未触及的 Bulk lane 沿用入队时的 Items:1 事件。

- [ ] **Step 3: 跑调度器 benchmark 记录前后对比**

Run: `go test ./internal/sched/ -run=^$ -bench='BenchmarkScheduler' -benchmem -benchtime=2s`
Expected: `BenchmarkSchedulerMixedPriorityBatch` ns/op 从基线 ~245807 大幅下降（目标低十位 µs 以内）、allocs/op 下降；`BenchmarkSchedulerEnqueueNextBatch` 不退化（~180 ns、4 allocs 量级）。前后数字记入提交信息。

- [ ] **Step 4: 提交**

```bash
git add internal/sched/scheduler.go
git commit -m "perf(transportv2/sched): emit one queue event per touched lane per batch

Removes per-item scheduler_queue O(B*N) observation scan in NextBatch.
Benchmark SchedulerMixedPriorityBatch: <before> -> <after> ns/op.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## PR 2 — 写路径每批分配复用

**文件：**
- 修改：`wire/writer.go`、`internal/conn/conn.go`
- 测试：`wire/writer_test.go`、`wire/benchmark_test.go`、`internal/conn/conn_test.go`

### Task 2.1：wire 增加借用调用者 net.Buffers 的 WriteFramesInto

**Files:**
- Modify: `wire/writer.go:44-54`
- Test: `wire/writer_test.go`

- [ ] **Step 1: 写失败测试 — WriteFramesInto 复用传入 buffers 且结果与 WriteFrames 等价**

在 `wire/writer_test.go` 末尾追加：

```go
func TestWriteFramesIntoReusesBuffers(t *testing.T) {
	var out bytes.Buffer
	buffers := make(net.Buffers, 0, 8)
	frame := Frame{
		Header: Header{Kind: core.FrameKindData, Priority: core.PriorityRaft},
		Body:   core.NewOwnedBuffer([]byte("abc"), nil),
	}

	if err := WriteFramesInto(&out, &buffers, []Frame{frame}, 1024); err != nil {
		t.Fatalf("WriteFramesInto() error = %v", err)
	}
	got, err := ReadFrame(bytes.NewReader(out.Bytes()), 1024)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	defer got.Body.Release()
	if string(got.Body.Bytes()) != "abc" {
		t.Fatalf("body = %q, want abc", got.Body.Bytes())
	}
	if cap(buffers) < 8 {
		t.Fatalf("cap(buffers) = %d, want caller capacity preserved (>=8)", cap(buffers))
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./wire/ -run TestWriteFramesIntoReusesBuffers -v`
Expected: 编译失败 — `WriteFramesInto` 未定义。

- [ ] **Step 3: 实现 WriteFramesInto，并让 WriteFrames 复用它**

替换 `wire/writer.go` 的 `WriteFrames`（45-54 行），新增 `WriteFramesInto`：

```go
// WriteFrames writes frames as one net.Buffers batch. It borrows every
// frame.Body.Bytes() for the duration of the call and does not release bodies.
func WriteFrames(w io.Writer, frames []Frame, maxBodyBytes int) error {
	var buffers net.Buffers
	return WriteFramesInto(w, &buffers, frames, maxBodyBytes)
}

// WriteFramesInto writes frames using the caller-provided net.Buffers as scratch.
// It resets buffers to zero length, appends each frame's header and borrowed body,
// and writes them as one batch. The caller's backing capacity is reused across calls.
// It borrows every frame.Body.Bytes() for the duration of the call and does not
// release bodies.
func WriteFramesInto(w io.Writer, buffers *net.Buffers, frames []Frame, maxBodyBytes int) error {
	*buffers = (*buffers)[:0]
	for _, frame := range frames {
		if err := AppendFrame(buffers, frame, maxBodyBytes); err != nil {
			return err
		}
	}
	_, err := buffers.WriteTo(w)
	return err
}
```

注意：`AppendFrame` 把 `EncodeHeader` 返回的 `[HeaderSize]byte` 取地址 append（`encoded[:]`），
该数组是 `AppendFrame` 的栈局部，逃逸到堆 —— 每帧仍有一次 header 分配，PR2 不动它（PR 范围是批级切片复用）。

- [ ] **Step 4: 运行 wire 全部测试确认通过**

Run: `go test ./wire/ -race -v`
Expected: PASS（`WriteFrames` 行为不变；新测试通过）。

- [ ] **Step 5: 提交**

```bash
git add wire/writer.go wire/writer_test.go
git commit -m "perf(transportv2/wire): add WriteFramesInto for caller-owned buffer reuse

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

### Task 2.2：conn.writeLoop 跨批复用 outbounds/frames/net.Buffers scratch

**Files:**
- Modify: `internal/conn/conn.go`（`writeLoop` 244-256 行；`writeOutboundBatch` 271-323 行）
- Test: `internal/conn/conn_test.go`

- [ ] **Step 1: 写失败测试 — 连续多批写出后所有帧完整可读**

先确认 `conn_test.go` 现有 import 与 harness 风格，再在末尾追加一个端到端式测试，
通过一对 `net.Pipe` 连接发送多批帧并在对端逐帧读出，验证复用 scratch 不串数据。
（若该文件已有等价的多批读写辅助，复用之；下面给出自包含版本。）

```go
func TestWriteLoopReusesScratchAcrossBatches(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	limits := core.Limits{
		MaxFrameBodyBytes:     1 << 20,
		MaxQueuedBytesPerConn: 1 << 20,
		MaxQueuedItemsPerConn: 256,
		MaxBatchBytes:         64,
		MaxBatchFrames:        4,
		WriteTimeout:          time.Second,
	}
	c := New(serverSide, Config{Limits: limits}, nil)
	c.Start()
	defer c.Close(nil)

	const total = 32
	go func() {
		for i := 0; i < total; i++ {
			payload := []byte{byte(i), byte(i), byte(i)}
			_ = c.Send(context.Background(), Outbound{
				Kind:     core.FrameKindData,
				Priority: core.PriorityRaft,
				Payload:  core.NewOwnedBuffer(payload, nil),
			})
		}
	}()

	r := clientSide
	for i := 0; i < total; i++ {
		_ = r.SetReadDeadline(time.Now().Add(2 * time.Second))
		frame, err := wire.ReadFrame(r, limits.MaxFrameBodyBytes)
		if err != nil {
			t.Fatalf("ReadFrame(%d) error = %v", i, err)
		}
		body := frame.Body.Bytes()
		if len(body) != 3 || body[0] != byte(i) || body[1] != byte(i) || body[2] != byte(i) {
			frame.Body.Release()
			t.Fatalf("frame %d body = %v, want three bytes of %d", i, body, i)
		}
		frame.Body.Release()
	}
}
```

- [ ] **Step 2: 运行测试确认失败或通过（基线行为）**

Run: `go test ./internal/conn/ -run TestWriteLoopReusesScratchAcrossBatches -race -v`
Expected: 当前实现下应 PASS（功能正确，只是每批分配）。该测试作为复用改造的回归护栏；
若 import 缺 `wire`/`net`/`time`/`context`，先补齐 import 使其编译通过再运行。

- [ ] **Step 3: 让 writeLoop 持有 scratch 并下传给 writeOutboundBatch**

修改 `writeLoop`（244-256 行），声明跨批复用的 scratch，并改 `writeOutboundBatch` 签名接收它：

```go
func (c *Conn) writeLoop() {
	defer close(c.writeDone)
	var (
		outbounds []Outbound
		frames    []wire.Frame
		buffers   net.Buffers
	)
	for {
		batch, err := c.scheduler.WaitBatch()
		if err != nil {
			return
		}
		var werr error
		outbounds, frames, werr = c.writeOutboundBatch(batch, outbounds, frames, &buffers)
		if werr != nil {
			c.shutdown(werr)
			return
		}
	}
}
```

- [ ] **Step 4: 重写 writeOutboundBatch 使用传入 scratch 并返回复用切片**

替换 `writeOutboundBatch`（271-323 行）。关键点：入口 `[:0]` 重置传入切片；
`flush` 改用 `wire.WriteFramesInto(c.raw, buffers, frames, ...)`；函数返回复用后的 `outbounds/frames`：

```go
func (c *Conn) writeOutboundBatch(items []sched.Item, outbounds []Outbound, frames []wire.Frame, buffers *net.Buffers) ([]Outbound, []wire.Frame, error) {
	outbounds = outbounds[:0]
	frames = frames[:0]
	batchBytes := 0

	flush := func() error {
		if len(outbounds) == 0 {
			return nil
		}
		if timeout := c.cfg.Limits.WriteTimeout; timeout > 0 {
			if err := c.raw.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
				releaseOutbounds(outbounds)
				return err
			}
		}
		if err := writeFramesInto(c.raw, buffers, frames, c.cfg.Limits.MaxFrameBodyBytes); err != nil {
			releaseOutbounds(outbounds)
			return err
		}
		for _, outbound := range outbounds {
			c.observeBytes("sent_bytes", outbound.Kind, outbound.Payload.Len())
		}
		releaseOutbounds(outbounds)
		outbounds = outbounds[:0]
		frames = frames[:0]
		batchBytes = 0
		return nil
	}

	for i, item := range items {
		outbound, ok := item.Value.(Outbound)
		if !ok {
			continue
		}
		if outbound.writeCtx != nil && outbound.writeCtx.Err() != nil {
			outbound.Payload.Release()
			c.pending.Delete(outbound.RequestID)
			c.observePendingRPC("ok")
			continue
		}
		if c.outboundBatchWouldExceed(len(outbounds), batchBytes, outbound) {
			if err := flush(); err != nil {
				releaseSchedItems(items[i:])
				return outbounds, frames, err
			}
		}
		outbounds = append(outbounds, outbound)
		frames = append(frames, outbound.toFrame())
		batchBytes += outbound.Payload.Len()
	}

	return outbounds, frames, flush()
}
```

- [ ] **Step 5: 把 conn 的 writeFrames 别名指向 WriteFramesInto 变体**

`conn.go:18` 当前是 `var writeFrames = wire.WriteFrames`。新增一个可注入的 into 变体，便于既有故障注入测试覆盖：

```go
var writeFramesInto = wire.WriteFramesInto
```

并删除不再使用的 `var writeFrames = wire.WriteFrames`（若 `conn_test.go` 有对 `writeFrames` 打桩的测试，
改为对 `writeFramesInto` 打桩；执行时用 grep 确认：`grep -n "writeFrames" internal/conn/*.go`）。

- [ ] **Step 6: 运行 conn 全部测试确认通过**

Run: `go test ./internal/conn/ -race -v`
Expected: PASS（含背压、关闭、写超时、故障注入路径）。

- [ ] **Step 7: 跑端到端并行 benchmark 记录 alloc 下降**

Run: `go test . -run=^$ -bench='BenchmarkTransportV2RPCParallel$' -benchmem -benchtime=2s`
Expected: allocs/op 较基线 41 下降；ns/op 不退化。数字记入提交信息。

- [ ] **Step 8: 提交**

```bash
git add internal/conn/conn.go internal/conn/conn_test.go
git commit -m "perf(transportv2/conn): reuse write batch scratch across writeLoop iterations

Hoists outbounds/frames/net.Buffers out of per-batch allocation in writeLoop.
Benchmark TransportV2RPCParallel allocs/op: <before> -> <after>.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## PR 3 — RPC 响应路径去拷贝 + 去 per-request goroutine

**前置说明（读代码后修正）：** `handleRPCResponse`（conn.go:361）与 `service.handle`（service.go:311）的
两处 `append([]byte(nil), ...)` 拷贝是**承重的** —— 它们把数据搬出"即将被释放的池化 slab / handler 复用切片"
跨越 `Response.Payload []byte` 这个 API 边界，不能在保持 `Handler` 签名不变的前提下消除。
PR3a 因此只消除**可安全消除**的那一处：`EncodeRPCResponse` 的裸 `make`，改用 slab 池
（该 buffer 在写出后由 `releaseOutbounds` 释放，生命周期闭合，可安全池化）。
PR3b 消除 per-request 的 `make(chan)` + `go`。

### Task 3a.1：EncodeRPCResponse 改用 slab 池

**Files:**
- Modify: `internal/conn/conn.go:430-436`（`EncodeRPCResponse`）
- Test: `internal/conn/conn_test.go`

- [ ] **Step 1: 写失败测试 — EncodeRPCResponse 返回正确内容且 Release 后归还池**

在 `conn_test.go` 末尾追加（验证内容正确性 + Release 幂等，间接验证池化不破坏语义）：

```go
func TestEncodeRPCResponseContentAndRelease(t *testing.T) {
	payload := []byte("response-body")
	buf := EncodeRPCResponse(wire.ResponseOK, payload)
	got := buf.Bytes()
	if len(got) != 1+len(payload) || got[0] != wire.ResponseOK || string(got[1:]) != "response-body" {
		t.Fatalf("EncodeRPCResponse bytes = %v, want status+payload", got)
	}
	buf.Release()
	buf.Release() // must be idempotent
}
```

- [ ] **Step 2: 运行测试确认通过（基线）**

Run: `go test ./internal/conn/ -run TestEncodeRPCResponseContentAndRelease -race -v`
Expected: PASS（当前实现内容正确）。该测试作为池化改造的回归护栏。

- [ ] **Step 3: 改 EncodeRPCResponse 用 slab 池**

替换 `EncodeRPCResponse`（430-436 行）。从 `buffer.DefaultSlabPool` 取一块 `1+len(payload)` 的 buffer
（池返回的 OwnedBuffer 带归还 release），写入 status+payload：

```go
// EncodeRPCResponse returns an owned RPC response body containing status then payload.
// The buffer is drawn from the shared slab pool and released after the response frame
// is written out (see releaseOutbounds in the write path).
func EncodeRPCResponse(status uint8, payload []byte) core.OwnedBuffer {
	buf := buffer.DefaultSlabPool.Get(1 + len(payload))
	body := buf.Bytes()
	body[0] = status
	copy(body[1:], payload)
	return buf
}
```

在 `conn.go` import 块补 `"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/buffer"`。
注意：`SlabPool.Get(n)` 对 `n<=0` 返回零值 OwnedBuffer；此处 `1+len(payload) >= 1`，恒为正，安全。

- [ ] **Step 4: 运行 conn 全部测试确认通过**

Run: `go test ./internal/conn/ -race -v`
Expected: PASS。

- [ ] **Step 5: 端到端回归 + benchmark**

Run: `go test . -run TestClientServer -race -count=1` 然后 `go test . -run=^$ -bench='BenchmarkTransportV2RPC$' -benchmem -benchtime=2s`
Expected: 端到端 PASS；`BenchmarkTransportV2RPC` allocs/op 较基线 42 减少（去掉一次 make）。数字记入提交信息。
（执行时先 `grep -rn "func TestClientServer" .` 确认端到端测试名前缀，按实际名跑。）

- [ ] **Step 6: 提交**

```bash
git add internal/conn/conn.go internal/conn/conn_test.go
git commit -m "perf(transportv2/conn): pool RPC response encode buffer

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

### Task 3b.1：Request 增加 Respond 回调，service 完成时直接回调

**Files:**
- Modify: `internal/rpc/service.go`（`Request` 19-25 行；`handle` 293-316 行；所有 `trySendResponse(req.Reply, …)` 调用点）
- Test: `internal/rpc/service_test.go`

- [ ] **Step 1: 写失败测试 — 带 Respond 回调的 Request 在 handler 完成后被回调一次**

在 `service_test.go` 末尾追加：

```go
func TestServiceInvokesRespondCallback(t *testing.T) {
	s := NewService(7, func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("echo:"), payload...), nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 4, MaxQueueBytes: 1 << 20}, nil)
	defer s.Stop()

	got := make(chan Response, 1)
	err := s.Enqueue(Request{
		Payload: core.CopyOwnedBuffer([]byte("hi")),
		Respond: func(resp Response) { got <- resp },
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	select {
	case resp := <-got:
		if resp.Err != nil || string(resp.Payload) != "echo:hi" {
			t.Fatalf("Respond got %+v, want echo:hi", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Respond callback not invoked")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/rpc/ -run TestServiceInvokesRespondCallback -v`
Expected: 编译失败 — `Request` 无 `Respond` 字段。

- [ ] **Step 3: 给 Request 加 Respond 字段**

修改 `Request`（19-25 行）：

```go
// Request is one service invocation owned by the service after a successful Enqueue.
type Request struct {
	// Payload carries the request bytes and must be released by the service owner.
	Payload core.OwnedBuffer
	// Reply optionally receives a copied response payload and terminal handler error.
	Reply chan Response
	// Respond, when non-nil, is invoked exactly once with the terminal response in
	// place of Reply. It runs on the executor worker goroutine; it must not block.
	Respond func(Response)
}
```

- [ ] **Step 4: 新增 deliver 辅助并替换所有终态投递点**

`Respond` 必须像 `Reply` 一样在所有终态被调用一次，否则 server 端响应会丢失。
在 `service.go` 新增统一辅助（放在 `trySendResponse` 附近，427 行后）：

```go
func deliver(req Request, resp Response) {
	if req.Respond != nil {
		req.Respond(resp)
		return
	}
	trySendResponse(req.Reply, resp)
}
```

将以下每处 `trySendResponse(req.Reply, X)` 改为 `deliver(req, X)`：
`Enqueue` stopped 分支（143 行）、`Stop` drain 分支（188 行）、`pump` 的 not-active 分支（220 行）、
`pump` 的 submit-fail 分支（240 行）。`handle` 末尾与 `serviceTask.run` panic 分支在 Step 5/6 单独处理。

- [ ] **Step 5: 改 handle 末尾用 deliver**

修改 `handle`（293-316 行）末尾的回复投递：

```go
	resp, err := s.handler(ctx, req.Payload.Bytes())
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = core.ErrTimeout
	}
	if req.Reply == nil && req.Respond == nil {
		return err
	}
	reply := Response{Payload: append([]byte(nil), resp...), Err: err}
	// Reply channels are required to be buffered by callers; non-blocking send keeps Stop from
	// waiting forever when the caller has abandoned a request. Respond runs inline on the worker.
	deliver(req, reply)
	return err
```

- [ ] **Step 6: 改 serviceTask.run panic 分支用 deliver**

修改 `serviceTask.run`（464-475 行）的 panic recover 分支：

```go
	defer func() {
		if recovered := recover(); recovered != nil {
			result = "panic"
			deliver(t.req, Response{
				Err: fmt.Errorf("transportv2: service handler panic: %v", recovered),
			})
		}
		s.observeTask(result, payloadLen, nonNegativeSince(started))
		s.observeInflight(int(s.inflight.Add(-1)))
		s.releaseToken()
		s.taskWG.Done()
	}()
```

- [ ] **Step 7: 运行 rpc 全部测试确认通过**

Run: `go test ./internal/rpc/ -race -v`
Expected: PASS（新测试通过；既有 Reply 路径测试不受影响，Respond 为 nil 时走原 trySendResponse）。

- [ ] **Step 8: 提交**

```bash
git add internal/rpc/service.go internal/rpc/service_test.go
git commit -m "feat(transportv2/rpc): add Respond callback to Request for goroutine-free reply

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

### Task 3b.2：server 用 Respond 回调替代 per-request goroutine + channel

**Files:**
- Modify: `server.go`（`dispatchRPCRequest` 242-256 行；删除 `sendRPCResponse` 258-277 行）
- Test: `client_server_test.go`

- [ ] **Step 1: 改 dispatchRPCRequest 用 Respond 回调直接写响应**

替换 `dispatchRPCRequest`（242-256 行），不再 `make(chan)` + `go`，改为传入 `Respond` 闭包，
闭包在 handler 完成时（executor worker 上）直接构造并 `Conn.Send` 响应帧：

```go
func (s *Server) dispatchRPCRequest(ctx context.Context, inbound conn.Inbound) {
	service := s.service(inbound.ServiceID)
	if service == nil {
		inbound.Payload.Release()
		s.sendRPCError(ctx, inbound, fmt.Errorf("transportv2: service %d not found", inbound.ServiceID))
		return
	}

	respond := func(resp rpc.Response) {
		select {
		case <-inbound.Conn.Done():
			return
		case <-s.ctx.Done():
			return
		default:
		}
		status := wire.ResponseOK
		payload := resp.Payload
		if resp.Err != nil {
			status = wire.ResponseErr
			payload = []byte(resp.Err.Error())
		}
		_ = inbound.Conn.Send(ctx, conn.Outbound{
			Kind:      core.FrameKindRPCResponse,
			Priority:  inbound.Priority,
			ServiceID: inbound.ServiceID,
			RequestID: inbound.RequestID,
			Payload:   conn.EncodeRPCResponse(status, payload),
		})
	}

	if err := service.Enqueue(rpc.Request{Payload: inbound.Payload, Respond: respond}); err != nil {
		s.sendRPCError(ctx, inbound, err)
	}
}
```

- [ ] **Step 2: 删除不再使用的 sendRPCResponse**

删除 `sendRPCResponse`（258-277 行）。`sendRPCError`（279-294 行）保留 —— 仍用于 service 缺失/enqueue 失败的同步错误。
执行时用 `grep -n sendRPCResponse server.go` 确认无残留引用后再删。

- [ ] **Step 3: 运行端到端测试确认通过**

Run: `go test . -race -count=1`
Expected: PASS（RPC 往返、错误、取消、并发场景）。
若有测试断言"响应在独立 goroutine 发送"的时序，按新模型（executor worker 内发送）调整断言。

- [ ] **Step 4: 端到端 benchmark 记录 goroutine/alloc 下降**

Run: `go test . -run=^$ -bench='BenchmarkTransportV2RPC$|BenchmarkTransportV2RPCParallel$' -benchmem -benchtime=2s`
Expected: allocs/op 较 PR3a 后进一步下降（去掉 per-request channel）；ns/op 不退化、并行场景可改善。
数字记入提交信息。

- [ ] **Step 5: 提交**

```bash
git add server.go client_server_test.go
git commit -m "perf(transportv2): respond to RPC on executor worker, drop per-request goroutine

Replaces per-request reply channel + goroutine with a Respond callback invoked
on the service executor worker. RPC allocs/op: <before> -> <after>.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## PR 4 — 读路径与 OwnedBuffer 分配优化

**前置说明（读代码后修正）：** `OwnedBuffer` 当前每次 `NewOwnedBuffer` 堆分配一个含 `sync.Once` 的
`ownedState`。本 PR 把 release 从 `sync.Once` 改为 `atomic` CAS，去掉 `Once` 的内嵌开销，
并保持既有语义契约：`Release` 幂等、Release 后 `Bytes()`→nil / `Len()`→0
（`core/types_test.go:TestOwnedBufferReleaseOnce` 与 `TestOwnedBufferBytesAndLenAreEmptyAfterRelease` 必须继续通过）。
`ownedState` 仍需堆分配以承载 `data`/`release` 指针（`OwnedBuffer` 按值传递且需共享释放状态），
故本 PR 的收益是去掉 `Once` 开销并为 release 路径减负，不是消灭 state 分配本身。

### Task 4.1：OwnedBuffer release 改用 atomic CAS

**Files:**
- Modify: `internal/core/types.go:222-269`
- Test: `internal/core/types_test.go`

- [ ] **Step 1: 写失败测试 — 并发 Release 只触发一次底层释放**

在 `core/types_test.go` 末尾追加并发幂等测试（race 下尤其有意义）：

```go
func TestOwnedBufferConcurrentReleaseReleasesOnce(t *testing.T) {
	var releases int32
	buf := NewOwnedBuffer([]byte("abc"), func([]byte) {
		atomic.AddInt32(&releases, 1)
	})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf.Release()
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&releases); got != 1 {
		t.Fatalf("releases = %d, want 1", got)
	}
	if buf.Bytes() != nil || buf.Len() != 0 {
		t.Fatalf("after release Bytes()/Len() = %v/%d, want nil/0", buf.Bytes(), buf.Len())
	}
}
```

在该测试文件 import 块补 `"sync"` 与 `"sync/atomic"`（执行时确认是否已存在）。

- [ ] **Step 2: 运行测试确认通过（基线 sync.Once 也满足）**

Run: `go test ./internal/core/ -run TestOwnedBufferConcurrentReleaseReleasesOnce -race -v`
Expected: PASS（当前 `sync.Once` 实现已并发安全）。该测试作为 CAS 改造的护栏。

- [ ] **Step 3: 把 ownedState 的 sync.Once 换成 atomic.Bool（CAS）**

替换 `internal/core/types.go` 的 `ownedState` 结构体与 `Release`（222-269 行）。
import 块用 `sync/atomic` 替换 `sync`（确认 `types.go` 内 `sync` 无其他用途 —— 执行时 `grep -n "sync\." internal/core/types.go`；
若仅 `sync.Once` 使用，则替换 import）：

```go
type ownedState struct {
	data     []byte
	release  func([]byte)
	released atomic.Bool
}

// OwnedBuffer carries payload bytes plus explicit release ownership.
type OwnedBuffer struct {
	state *ownedState
}

// NewOwnedBuffer wraps caller-owned bytes with an optional release callback.
func NewOwnedBuffer(data []byte, release func([]byte)) OwnedBuffer {
	return OwnedBuffer{state: &ownedState{data: data, release: release}}
}

// CopyOwnedBuffer copies bytes into a new owned buffer.
func CopyOwnedBuffer(data []byte) OwnedBuffer {
	copied := append([]byte(nil), data...)
	return NewOwnedBuffer(copied, nil)
}

// Bytes returns the current payload bytes.
func (b OwnedBuffer) Bytes() []byte {
	if b.state == nil {
		return nil
	}
	if b.state.released.Load() {
		return nil
	}
	return b.state.data
}

// Len returns the payload length.
func (b OwnedBuffer) Len() int {
	return len(b.Bytes())
}

// Release releases the payload at most once.
func (b OwnedBuffer) Release() {
	if b.state == nil {
		return
	}
	if !b.state.released.CompareAndSwap(false, true) {
		return
	}
	data := b.state.data
	b.state.data = nil
	if b.state.release != nil {
		b.state.release(data)
	}
}
```

注意：`Bytes()` 增加 `released.Load()` 检查以保证 Release 后返回 nil（原实现靠 `data=nil`，
CAS 版同样把 `data` 置 nil，但置 nil 与读取存在并发窗口，故 `Bytes()` 显式查 released 标志更稳）。

- [ ] **Step 4: 运行 core 全部测试确认通过**

Run: `go test ./internal/core/ -race -v`
Expected: PASS（`TestOwnedBufferReleaseOnce`、`TestOwnedBufferBytesAndLenAreEmptyAfterRelease`、新并发测试全绿）。

- [ ] **Step 5: 全库回归 + slab/read benchmark**

Run: `go test ./... -race -count=1` 然后 `go test ./internal/buffer/ ./wire/ -run=^$ -bench='BenchmarkSlabPoolGetRelease|BenchmarkReadFrame' -benchmem -benchtime=2s`
Expected: 全库 PASS；`BenchmarkSlabPoolGetRelease`、`BenchmarkReadFrame1KiB` ns/op 不退化、有望小幅下降。数字记入提交信息。

- [ ] **Step 6: 提交**

```bash
git add internal/core/types.go internal/core/types_test.go
git commit -m "perf(transportv2/core): release OwnedBuffer via atomic CAS instead of sync.Once

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## PR 5 — 调度器锁模型（enqueue/dequeue 分离争用）

**前置说明（读代码后定方向）：** spec 列了两个候选（per-lane 无锁 MPSC vs enqueue/dequeue 分离锁）。
本计划采用**风险更低、收益明确**的方向：保留单 `mu`+`Cond` 的正确性骨架，但把 `Enqueue` 的观测事件构造
（`snapshotQueueLocked`/`snapshotLaneQueueLocked` + 两次 `observeEnqueue`）移出临界区，缩短持锁时间，
降低 fanout 下入队与 writeLoop 出队的锁竞争窗口。这是对 DRR/Cond 零改动的安全收敛。
（真正的无锁 MPSC 列为后续可选项，不在本 PR —— 它会触碰 DRR 权重与批次子组顺序，风险过高。）

### Task 5.1：把 Enqueue 成功路径的观测移出临界区

**Files:**
- Modify: `internal/sched/scheduler.go`（`Enqueue` 156-176 行）
- Test: `internal/sched/scheduler_test.go`

- [ ] **Step 1: 写失败测试 — 高并发 enqueue 下计数不丢且观测事件齐全**

在 `scheduler_test.go` 末尾追加并发入队测试，断言所有成功入队都被记账、且每次成功产生一个
`scheduler_admission ok` 事件（观测语义不因移出临界区而丢失）：

```go
func TestConcurrentEnqueueAccountsAllItems(t *testing.T) {
	observer := &recordingObserver{}
	s := New(Config{
		MaxItems:       100000,
		MaxBytes:       1 << 30,
		MaxBatchFrames: 64,
		MaxBatchBytes:  1 << 20,
		Observer:       observer,
		SourceID:       5,
	})

	const goroutines, per = 16, 500
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if err := s.Enqueue(context.Background(), Item{
					Priority: core.PriorityRaft, Bytes: 1, Value: i,
				}); err != nil {
					t.Errorf("Enqueue() error = %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	s.mu.Lock()
	gotItems := s.queuedItems
	s.mu.Unlock()
	if gotItems != goroutines*per {
		t.Fatalf("queuedItems = %d, want %d", gotItems, goroutines*per)
	}

	okCount := 0
	for _, e := range observer.snapshot() {
		if e.Name == "scheduler_admission" && e.Result == "ok" {
			okCount++
		}
	}
	if okCount != goroutines*per {
		t.Fatalf("scheduler_admission ok count = %d, want %d", okCount, goroutines*per)
	}
}
```

- [ ] **Step 2: 运行测试确认通过（基线）**

Run: `go test ./internal/sched/ -run TestConcurrentEnqueueAccountsAllItems -race -v`
Expected: PASS（当前实现已正确）。作为锁改造的并发护栏。

- [ ] **Step 3: 缩短 Enqueue 成功路径的持锁时间**

修改 `Enqueue` 入队成功分支（PR1 改后的 157-169 行）：在锁内只做记账与 `Signal`，
快照与 `observeEnqueue` 移到 `Unlock` 之后。注意快照值需在锁内读出到局部变量再于锁外上报：

```go
	for i := range s.lanes {
		if s.lanes[i].priority == item.Priority {
			item.enqueuedAt = time.Now()
			s.lanes[i].queue = append(s.lanes[i].queue, item)
			s.lanes[i].items++
			s.lanes[i].bytes += queueBytes(item)
			s.queuedItems++
			s.queuedBytes += int64(item.Bytes)
			snapshot := s.snapshotQueueLocked()
			laneSnapshot := s.snapshotLaneQueueLocked(item.Priority)
			s.cond.Signal()
			s.mu.Unlock()
			s.observeEnqueue("ok", item, snapshot, laneSnapshot)
			return nil
		}
	}
```

说明：此结构在 PR1 后已是"锁内取快照值、`Unlock`、锁外 `observeEnqueue`"。
PR5 的核心是确认并固化该模式 —— `observeEnqueue`（触发用户 Observer 回调，可能慢）绝不在持锁时调用。
检查 `Enqueue` 其余失败分支（stopped/canceled/full）是否也已 `Unlock` 后再 `observeEnqueue`：
当前代码（128-176 行）每个失败分支都先取快照、`Unlock`、再 observe —— 保持不变即可。
若发现任何分支在持锁时调用 `observeEnqueue` 或 `s.observer.ObserveTransport`，移到 `Unlock` 之后。

- [ ] **Step 4: 运行 sched 全部测试 + race 确认通过**

Run: `go test ./internal/sched/ -race -count=1 -v`
Expected: PASS（含 PR1 的观测断言与本 PR 并发测试）。

- [ ] **Step 5: 跑并行背压 benchmark 记录锁争用改善**

Run: `go test . -run=^$ -bench='BenchmarkTransportV2SendParallelWithBackpressure$' -benchmem -benchtime=2s`
对照（可选）：`go test . -run=^$ -bench='BenchmarkTransportV2SendParallelWithBackpressure$' -benchtime=2s -mutexprofile=mutex.out` 后
`go tool pprof -top mutex.out` 确认 `Scheduler.mu` 争用占比下降。
Expected: ns/op 不退化、并行吞吐有望改善；mutex profile 中调度器锁争用下降。数字记入提交信息。

- [ ] **Step 6: 提交**

```bash
git add internal/sched/scheduler.go internal/sched/scheduler_test.go
git commit -m "perf(transportv2/sched): keep observer callbacks out of enqueue critical section

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

### Task 5.2：PR1-5 后的系统级压测对照

**Files:**
- 无代码改动；产出记录写入 `docs/development/WKSIM_STRESS_FINDINGS.md`

- [ ] **Step 1: 跑三节点真实 QPS 压测**

Run: `./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 4000`
Expected: 拿到 actual QPS、p99、各节点 CPU 峰值，与 `WKSIM_STRESS_FINDINGS.md` 2026-06-12 基线
（1945.6 actual QPS、p99 5348ms、node CPU 峰 375/362/285%）对照。

- [ ] **Step 2: 采集热路径 pprof 确认写路径/调度器开销下降**

Run: 按 `WKSIM_STRESS_FINDINGS.md` 记录的 live pprof 方法采集 15s CPU profile。
Expected: `conn.writeLoop -> writeOutbound/WriteFramesInto -> net.Buffers.WriteTo -> writev` 的累计 CPU 占比
较基线 ~1/3 下降；调度器观测扫描从热点消失。

- [ ] **Step 3: 记录结果**

在 `docs/development/WKSIM_STRESS_FINDINGS.md` 追加一个 "2026-06-xx transportv2 hot-path surgical refactor" 条目，
写明场景、evidence 路径、前后对比、剩余瓶颈归类（按既有条目格式）。

- [ ] **Step 4: 提交**

```bash
git add docs/development/WKSIM_STRESS_FINDINGS.md
git commit -m "docs(stress): record transportv2 hot-path refactor three-node results

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## 完成标准

- 五个 PR 全部合入，每个 PR 的 benchmark 前后数字记入其提交信息。
- `go test ./... -race` 在 `pkg/transportv2` 全绿。
- 端到端 RPC 往返 allocs/op 较基线 42 显著下降（目标 ~15 以下）。
- `BenchmarkSchedulerMixedPriorityBatch` 较基线 245807 ns/批 大幅下降。
- 三节点压测中 `conn.writeLoop` 系列 CPU 占比下降，调度器观测扫描不再是热点。
- `clusterv2/net` 与 `internalv2/app/observability.go` 消费端无需改动（公共 API 与观测语义保持）。
