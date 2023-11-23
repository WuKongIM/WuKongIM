package grpcpool

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

func TestNew(t *testing.T) {
	p, err := New(func() (*grpc.ClientConn, error) {
		return grpc.Dial("example.com", grpc.WithInsecure())
	}, 1, 3, 0)
	if err != nil {
		t.Errorf("The pool returned an error: %s", err.Error())
	}
	if a := p.Available(); a != 3 {
		t.Errorf("The pool available was %d but should be 3", a)
	}
	if a := p.Capacity(); a != 3 {
		t.Errorf("The pool capacity was %d but should be 3", a)
	}

	// Get a client
	client, err := p.Get(context.Background())
	if err != nil {
		t.Errorf("Get returned an error: %s", err.Error())
	}
	if client == nil {
		t.Error("client was nil")
	}
	if a := p.Available(); a != 2 {
		t.Errorf("The pool available was %d but should be 2", a)
	}
	if a := p.Capacity(); a != 3 {
		t.Errorf("The pool capacity was %d but should be 3", a)
	}

	// Return the client
	err = client.Close()
	if err != nil {
		t.Errorf("Close returned an error: %s", err.Error())
	}
	if a := p.Available(); a != 3 {
		t.Errorf("The pool available was %d but should be 3", a)
	}
	if a := p.Capacity(); a != 3 {
		t.Errorf("The pool capacity was %d but should be 3", a)
	}

	// Attempt to return the client again
	err = client.Close()
	if err != ErrAlreadyClosed {
		t.Errorf("Expected error \"%s\" but got \"%s\"",
			ErrAlreadyClosed.Error(), err.Error())
	}

	// Take 3 clients
	cl1, err1 := p.Get(context.Background())
	cl2, err2 := p.Get(context.Background())
	cl3, err3 := p.Get(context.Background())
	if err1 != nil {
		t.Errorf("Err1 was not nil: %s", err1.Error())
	}
	if err2 != nil {
		t.Errorf("Err2 was not nil: %s", err2.Error())
	}
	if err3 != nil {
		t.Errorf("Err3 was not nil: %s", err3.Error())
	}

	if a := p.Available(); a != 0 {
		t.Errorf("The pool available was %d but should be 0", a)
	}
	if a := p.Capacity(); a != 3 {
		t.Errorf("The pool capacity was %d but should be 3", a)
	}

	// Returning all of them
	err1 = cl1.Close()
	if err1 != nil {
		t.Errorf("Close returned an error: %s", err1.Error())
	}
	err2 = cl2.Close()
	if err2 != nil {
		t.Errorf("Close returned an error: %s", err2.Error())
	}
	err3 = cl3.Close()
	if err3 != nil {
		t.Errorf("Close returned an error: %s", err3.Error())
	}
}

func TestTimeout(t *testing.T) {
	p, err := New(func() (*grpc.ClientConn, error) {
		return grpc.Dial("example.com", grpc.WithInsecure())
	}, 1, 1, 0)
	if err != nil {
		t.Errorf("The pool returned an error: %s", err.Error())
	}

	_, err = p.Get(context.Background())
	if err != nil {
		t.Errorf("Get returned an error: %s", err.Error())
	}
	if a := p.Available(); a != 0 {
		t.Errorf("The pool available was %d but expected 0", a)
	}

	// We want to fetch a second one, with a timeout. If the timeout was
	// ommitted, the pool would wait indefinitely as it'd wait for another
	// client to get back into the queue
	ctx, _ := context.WithDeadline(context.Background(), time.Now().Add(10*time.Millisecond))
	_, err2 := p.Get(ctx)
	if err2 != ErrTimeout {
		t.Errorf("Expected error \"%s\" but got \"%s\"", ErrTimeout, err2.Error())
	}
}

func TestMaxLifeDuration(t *testing.T) {
	p, err := New(func() (*grpc.ClientConn, error) {
		return grpc.Dial("example.com", grpc.WithInsecure())
	}, 1, 1, 0, 1)
	if err != nil {
		t.Errorf("The pool returned an error: %s", err.Error())
	}

	c, err := p.Get(context.Background())
	if err != nil {
		t.Errorf("Get returned an error: %s", err.Error())
	}

	// The max life of the connection was very low (1ns), so when we close
	// the connection it should get marked as unhealthy
	if err := c.Close(); err != nil {
		t.Errorf("Close returned an error: %s", err.Error())
	}
	if !c.unhealthy {
		t.Errorf("the connection should've been marked as unhealthy")
	}

	// Let's also make sure we don't prematurely close the connection
	count := 0
	p, err = New(func() (*grpc.ClientConn, error) {
		count++
		return grpc.Dial("example.com", grpc.WithInsecure())
	}, 1, 1, 0, time.Minute)
	if err != nil {
		t.Errorf("The pool returned an error: %s", err.Error())
	}

	for i := 0; i < 3; i++ {
		c, err = p.Get(context.Background())
		if err != nil {
			t.Errorf("Get returned an error: %s", err.Error())
		}

		// The max life of the connection is high, so when we close
		// the connection it shouldn't be marked as unhealthy
		if err := c.Close(); err != nil {
			t.Errorf("Close returned an error: %s", err.Error())
		}
		if c.unhealthy {
			t.Errorf("the connection shouldn't have been marked as unhealthy")
		}
	}

	// Count should have been 1 as dial function should only have been called once
	if count > 1 {
		t.Errorf("Dial function has been called multiple times")
	}

}

func TestPoolClose(t *testing.T) {
	p, err := New(func() (*grpc.ClientConn, error) {
		return grpc.Dial("example.com", grpc.WithInsecure())
	}, 1, 1, 0)
	if err != nil {
		t.Errorf("The pool returned an error: %s", err.Error())
	}

	c, err := p.Get(context.Background())
	if err != nil {
		t.Errorf("Get returned an error: %s", err.Error())
	}

	cc := c.ClientConn
	if err := c.Close(); err != nil {
		t.Errorf("Close returned an error: %s", err.Error())
	}

	// Close pool should close all underlying gRPC client connections
	p.Close()

	if cc.GetState() != connectivity.Shutdown {
		t.Errorf("Returned connection was not closed, underlying connection is not in shutdown state")
	}
}

func TestContextCancelation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewWithContext(ctx, func(ctx context.Context) (*grpc.ClientConn, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		default:
			return grpc.Dial("example.com", grpc.WithInsecure())
		}

	}, 1, 1, 0)

	if err != context.Canceled {
		t.Errorf("Returned error was not context.Canceled, but the context did cancel before the invocation")
	}
}
func TestContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Microsecond)
	defer cancel()

	_, err := NewWithContext(ctx, func(ctx context.Context) (*grpc.ClientConn, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		// wait for the deadline to pass
		case <-time.After(time.Millisecond):
			return grpc.Dial("example.com", grpc.WithInsecure())
		}

	}, 1, 1, 0)

	if err != context.DeadlineExceeded {
		t.Errorf("Returned error was not context.DeadlineExceeded, but the context was timed out before the initialization")
	}
}

func TestGetContextTimeout(t *testing.T) {
	p, err := New(func() (*grpc.ClientConn, error) {
		return grpc.Dial("example.com", grpc.WithInsecure())
	}, 1, 1, 0)

	if err != nil {
		t.Errorf("The pool returned an error: %s", err.Error())
	}

	// keep busy the available conn
	_, _ = p.Get(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), time.Microsecond)
	defer cancel()

	// wait for the deadline to pass
	time.Sleep(time.Millisecond)
	_, err = p.Get(ctx)
	if err != ErrTimeout { // it should be context.DeadlineExceeded
		t.Errorf("Returned error was not ErrTimeout, but the context was timed out before the Get invocation")
	}
}

func TestGetContextFactoryTimeout(t *testing.T) {
	p, err := NewWithContext(context.Background(), func(ctx context.Context) (*grpc.ClientConn, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		// wait for the deadline to pass
		case <-time.After(time.Millisecond):
			return grpc.Dial("example.com", grpc.WithInsecure())
		}

	}, 1, 1, 0)

	if err != nil {
		t.Errorf("The pool returned an error: %s", err.Error())
	}

	// mark as unhealty the available conn
	c, err := p.Get(context.Background())
	if err != nil {
		t.Errorf("Get returned an error: %s", err.Error())
	}
	c.Unhealthy()
	c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Microsecond)
	defer cancel()

	_, err = p.Get(ctx)
	if err != context.DeadlineExceeded {
		t.Errorf("Returned error was not context.DeadlineExceeded, but the context was timed out before the Get invocation")
	}
}
