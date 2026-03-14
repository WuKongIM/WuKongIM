package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/client"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

const (
	benchLatBufCap = 5_000_000 // Max latency samples per pair
	benchWarmupN   = 5         // Messages per pair during warmup
)

// benchPair represents a sender/receiver pair.
type benchPair struct {
	id       int
	sender   *client.Client
	receiver *client.Client
	channel  *client.Channel
	latBuf   []int64 // Latency samples in nanoseconds (written only by receiver goroutine)
}

// benchEngine orchestrates the benchmark run.
type benchEngine struct {
	cfg     *benchConfig
	pairs   []*benchPair
	metrics *benchMetrics
	pairSeq atomic.Int64
}

func newBenchEngine(cfg *benchConfig) *benchEngine {
	return &benchEngine{
		cfg: cfg,
		metrics: &benchMetrics{
			payloadLen: cfg.PayloadLen,
		},
	}
}

// connectAll creates and connects all sender/receiver pairs.
func (e *benchEngine) connectAll() error {
	e.pairs = make([]*benchPair, e.cfg.Pairs)
	nAddrs := len(e.cfg.Addrs)

	for i := 0; i < e.cfg.Pairs; i++ {
		id := e.pairSeq.Add(1)
		senderUID := fmt.Sprintf("bench_s%d", id)
		receiverUID := fmt.Sprintf("bench_r%d", id)

		var senderAddr, receiverAddr string
		if e.cfg.isCluster() {
			senderAddr = e.cfg.Addrs[i%nAddrs]
			receiverAddr = e.cfg.Addrs[(i+1)%nAddrs]
		} else {
			senderAddr = e.cfg.Addrs[0]
			receiverAddr = e.cfg.Addrs[0]
		}

		sender := client.New(senderAddr, client.WithUID(senderUID))
		if err := sender.Connect(); err != nil {
			e.cleanup()
			return fmt.Errorf("sender %d connect to %s: %w", i, senderAddr, err)
		}

		receiver := client.New(receiverAddr, client.WithUID(receiverUID))
		if err := receiver.Connect(); err != nil {
			sender.Close()
			e.cleanup()
			return fmt.Errorf("receiver %d connect to %s: %w", i, receiverAddr, err)
		}

		ch := client.NewChannel(receiverUID, wkproto.ChannelTypePerson)

		cap := benchLatBufCap
		if e.cfg.Rate > 0 {
			estimated := int(float64(e.cfg.Rate) * e.cfg.Duration.Seconds() * 1.1)
			if estimated < cap {
				cap = estimated
			}
		}

		e.pairs[i] = &benchPair{
			id:       i,
			sender:   sender,
			receiver: receiver,
			channel:  ch,
			latBuf:   make([]int64, 0, cap),
		}

		fmt.Printf("\r  %sConnecting...%s %d/%d pairs", colorCyan, colorReset, i+1, e.cfg.Pairs)
	}
	fmt.Println()
	return nil
}

// warmup sends a few messages per pair to initialize channels and caches.
func (e *benchEngine) warmup() error {
	fmt.Printf("  %sWarming up...%s\n", colorCyan, colorReset)

	for _, p := range e.pairs {
		recvCh := make(chan struct{}, 1)
		p.receiver.SetOnRecv(func(recv *wkproto.RecvPacket) error {
			select {
			case recvCh <- struct{}{}:
			default:
			}
			return nil
		})

		payload := make([]byte, e.cfg.PayloadLen)
		for range benchWarmupN {
			sendOpts := e.buildSendOptions()
			if err := p.sender.SendMessage(p.channel, payload, sendOpts...); err != nil {
				return fmt.Errorf("warmup send pair %d: %w", p.id, err)
			}
			if err := p.sender.Flush(); err != nil {
				return fmt.Errorf("warmup flush pair %d: %w", p.id, err)
			}
			select {
			case <-recvCh:
			case <-time.After(10 * time.Second):
				return fmt.Errorf("warmup recv timeout pair %d", p.id)
			}
		}
	}

	if e.cfg.Warmup > 0 {
		time.Sleep(e.cfg.Warmup)
	}

	return nil
}

// run executes the main benchmark loop.
func (e *benchEngine) run() error {
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.Duration)
	defer cancel()

	// Set up receive callbacks with latency measurement.
	for _, p := range e.pairs {
		pair := p // capture
		pair.receiver.SetOnRecv(func(recv *wkproto.RecvPacket) error {
			e.metrics.totalRecv.Add(1)
			// Extract timestamp from first 8 bytes if payload is large enough.
			payload := recv.Payload
			if len(payload) >= 8 {
				sentNano := int64(binary.BigEndian.Uint64(payload[:8]))
				lat := time.Now().UnixNano() - sentNano
				if lat > 0 && len(pair.latBuf) < benchLatBufCap {
					pair.latBuf = append(pair.latBuf, lat)
				}
			}
			return nil
		})
	}

	fmt.Printf("  %sBenchmarking...%s\n", colorCyan, colorReset)

	start := time.Now()

	var wg sync.WaitGroup
	for _, p := range e.pairs {
		wg.Add(1)
		go func(pair *benchPair) {
			defer wg.Done()
			e.sendLoop(ctx, pair)
		}(p)
	}

	// Progress ticker.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	for {
		select {
		case <-ticker.C:
			sent := e.metrics.totalSent.Load()
			recv := e.metrics.totalRecv.Load()
			elapsed := time.Since(start).Seconds()
			fmt.Printf("\r  %s[%.0fs]%s sent: %s  recv: %s  rate: %.0f msgs/s",
				colorGray, elapsed, colorReset,
				commaFmt(sent), commaFmt(recv),
				float64(sent)/elapsed)
		case <-done:
			e.metrics.elapsed = time.Since(start)
			fmt.Println()
			// Flush all pairs to push remaining buffered messages.
			for _, p := range e.pairs {
				p.sender.Flush()
			}
			return nil
		}
	}
}

// sendLoop runs in a goroutine, sending messages for one pair.
func (e *benchEngine) sendLoop(ctx context.Context, pair *benchPair) {
	payload := make([]byte, e.cfg.PayloadLen)
	sendOpts := e.buildSendOptions()

	if e.cfg.Rate > 0 {
		interval := time.Second / time.Duration(e.cfg.Rate)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				binary.BigEndian.PutUint64(payload[:8], uint64(time.Now().UnixNano()))
				if err := pair.sender.SendMessage(pair.channel, payload, sendOpts...); err != nil {
					e.metrics.totalSendErr.Add(1)
					continue
				}
				e.metrics.totalSent.Add(1)
			}
		}
	}

	// Unlimited rate.
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		binary.BigEndian.PutUint64(payload[:8], uint64(time.Now().UnixNano()))
		if err := pair.sender.SendMessage(pair.channel, payload, sendOpts...); err != nil {
			e.metrics.totalSendErr.Add(1)
			continue
		}
		e.metrics.totalSent.Add(1)
	}
}

// buildSendOptions returns common send options based on config.
func (e *benchEngine) buildSendOptions() []client.SendOption {
	var opts []client.SendOption
	if e.cfg.NoEncrypt {
		opts = append(opts, client.SendOptionWithNoEncrypt(true))
	}
	if e.cfg.NoPersist {
		opts = append(opts, client.SendOptionWithNoPersist(true))
	}
	return opts
}

// drain waits for remaining messages to be received (up to 5 seconds).
func (e *benchEngine) drain() {
	sent := e.metrics.totalSent.Load()
	recv := e.metrics.totalRecv.Load()
	if recv >= sent {
		return
	}
	fmt.Printf("  %sDraining...%s (waiting for %s messages)\n",
		colorCyan, colorReset, commaFmt(sent-recv))

	deadline := time.After(5 * time.Second)
	for e.metrics.totalRecv.Load() < sent {
		select {
		case <-deadline:
			return
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// cleanup closes all client connections.
func (e *benchEngine) cleanup() {
	for _, p := range e.pairs {
		if p == nil {
			continue
		}
		if p.sender != nil {
			p.sender.Close()
		}
		if p.receiver != nil {
			p.receiver.Close()
		}
	}
}

// runBench is the main entry point for the benchmark.
func runBench(cfg *benchConfig) error {
	engine := newBenchEngine(cfg)

	// Connect.
	if err := engine.connectAll(); err != nil {
		return err
	}
	defer engine.cleanup()

	// Warmup.
	if err := engine.warmup(); err != nil {
		return err
	}

	// Run benchmark.
	if err := engine.run(); err != nil {
		return err
	}

	// Drain remaining messages.
	engine.drain()

	// Finalize metrics.
	engine.metrics.finalize(engine.pairs)

	// Print report.
	printBenchReport(cfg, engine.metrics)

	return nil
}
