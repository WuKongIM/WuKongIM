package channelmeta

import "github.com/WuKongIM/WuKongIM/pkg/channel"

type activationCall struct {
	done       chan struct{}
	meta       channel.Meta
	err        error
	panicked   bool
	panicValue any
}

// RunSingleflight coalesces concurrent activation loads for the same channel key.
func (c *ActivationCache) RunSingleflight(key channel.ChannelKey, fn func() (channel.Meta, error)) (channel.Meta, error) {
	if c == nil {
		return fn()
	}
	c.mu.Lock()
	if c.calls == nil {
		c.calls = make(map[channel.ChannelKey]*activationCall)
	}
	if call, ok := c.calls[key]; ok {
		c.mu.Unlock()
		<-call.done
		if call.panicked {
			panic(call.panicValue)
		}
		return call.meta, call.err
	}
	call := &activationCall{done: make(chan struct{})}
	c.calls[key] = call
	c.mu.Unlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			call.panicked = true
			call.panicValue = recovered
			c.finishActivationCall(key, call)
			panic(recovered)
		}
		c.finishActivationCall(key, call)
	}()

	call.meta, call.err = fn()
	return call.meta, call.err
}

func (c *ActivationCache) finishActivationCall(key channel.ChannelKey, call *activationCall) {
	close(call.done)
	c.mu.Lock()
	if c.calls[key] == call {
		delete(c.calls, key)
	}
	c.mu.Unlock()
}
