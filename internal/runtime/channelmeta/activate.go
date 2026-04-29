package channelmeta

import "github.com/WuKongIM/WuKongIM/pkg/channel"

type activationCall struct {
	done       chan struct{}
	meta       channel.Meta
	result     MetaRefreshResult
	err        error
	panicked   bool
	panicValue any
}

// RunSingleflight coalesces concurrent activation loads for the same key, source scope, and generation.
func (c *ActivationCache) RunSingleflight(key channel.ChannelKey, business bool, fn func(ActivationCacheGeneration) (channel.Meta, MetaRefreshResult, error)) (channel.Meta, MetaRefreshResult, error) {
	if c == nil {
		return fn(ActivationCacheGeneration{})
	}
	c.mu.Lock()
	if c.calls == nil {
		c.calls = make(map[activationCallKey]*activationCall)
	}
	callKey := activationCallKey{
		key:        key,
		business:   business,
		generation: c.currentGenerationLocked(key),
	}
	if call, ok := c.calls[callKey]; ok {
		c.mu.Unlock()
		<-call.done
		if call.panicked {
			panic(call.panicValue)
		}
		return call.meta, call.result, call.err
	}
	call := &activationCall{done: make(chan struct{})}
	c.calls[callKey] = call
	c.mu.Unlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			call.panicked = true
			call.panicValue = recovered
			c.finishActivationCall(callKey, call)
			panic(recovered)
		}
		c.finishActivationCall(callKey, call)
	}()

	call.meta, call.result, call.err = fn(callKey.generation)
	return call.meta, call.result, call.err
}

func (c *ActivationCache) finishActivationCall(key activationCallKey, call *activationCall) {
	close(call.done)
	c.mu.Lock()
	if c.calls[key] == call {
		delete(c.calls, key)
	}
	c.mu.Unlock()
}
