package reactor

import "github.com/valyala/fastrand"

type ReadyState struct {
	processing          bool // 处理中
	willRetry           bool // 将要重试
	retryTick           int  // 重试计时，超过一定tick数后，将会重试
	retryCount          int  // 连续重试次数
	retryIntervalTick   int  // 默认配置的重试间隔
	currentIntervalTick int  // 当前重试间隔tick数，默认为retryIntervalTick的值，retryTick达到这个值后才重试
}

// NewReadyState retryIntervalTick为默认重试间隔
func NewReadyState(retryIntervalTick int) *ReadyState {

	return &ReadyState{
		retryIntervalTick:   retryIntervalTick,
		currentIntervalTick: retryIntervalTick,
	}
}

// 开始处理
func (r *ReadyState) StartProcessing() {
	r.processing = true
}

// 处理成功
func (r *ReadyState) ProcessSuccess() {
	r.processing = false
	r.willRetry = false
	r.retryCount = 0
	r.currentIntervalTick = r.retryIntervalTick
}

// 处理失败
func (r *ReadyState) ProcessFail() {
	r.willRetry = true
	r.retryTick = 0
	r.retryCount++

	// 重试间隔指数退避
	r.currentIntervalTick = tickExponentialBackoff(r.retryCount, r.retryIntervalTick, r.retryIntervalTick*10)
}

// 是否达到最大重试次数
func (r *ReadyState) IsMaxRetry() bool {
	return r.retryCount >= 10
}

// 是处理中
func (r *ReadyState) IsProcessing() bool {

	return r.processing
}

// 是否在重试
func (r *ReadyState) IsRetry() bool {
	return r.willRetry
}

// tick
func (r *ReadyState) Tick() {
	if r.willRetry {
		r.retryTick++
		if r.retryTick >= r.currentIntervalTick {
			r.willRetry = false
			r.retryTick = 0
			r.processing = false
		}
	}
}

func (r *ReadyState) Reset() {
	r.processing = false
	r.willRetry = false
	r.retryTick = 0
	r.retryCount = 0
	r.currentIntervalTick = r.retryIntervalTick
}

// tickExponentialBackoff 函数，根据重试次数返回延迟时间
// retries 重试次数
// baseDelay 基础延迟时间
// maxDelay 最大延迟时间，返回的延迟时间不会超过这个值
func tickExponentialBackoff(retries int, baseDelay, maxDelay int) int {

	// 重试次数小于3，返回基础延迟时间
	if retries < 3 {
		return baseDelay
	}

	// 超过三次后按照指数级延迟 计算指数退避延迟时间
	exp := retries - 3 + 1
	delay := baseDelay * (1 << exp) // 2^exp * baseDelay

	// 可能添加一个随机因子，防止所有请求同时重试
	p := float64(fastrand.Uint32()) / (1 << 32)
	jitter := int(p * float64(delay)) // 抖动范围，抖动范围为0~delay
	delay = delay + jitter

	// 限制最大延迟时间
	if delay > maxDelay {
		delay = maxDelay
	}

	return delay
}
