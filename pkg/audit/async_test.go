package audit

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type countingLogger struct {
	logs atomic.Int32
}

func (c *countingLogger) Log(_ context.Context, _ Event) error {
	c.logs.Add(1)
	return nil
}
func (c *countingLogger) Query(context.Context, QueryFilter) ([]Event, error) { return nil, nil }
func (c *countingLogger) Count(context.Context, QueryFilter) (int64, error)   { return 0, nil }

func TestAsyncLogger_DrainsOnClose(t *testing.T) {
	cl := &countingLogger{}
	a := NewAsyncLogger(cl, 64, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < 10; i++ {
		_ = a.Log(context.Background(), Event{Method: "GET", Path: "/x"})
	}
	a.Close()
	if got := cl.logs.Load(); got != 10 {
		t.Errorf("inner Log called %d times, want 10", got)
	}
	if a.Dropped() != 0 {
		t.Errorf("Dropped() = %d, want 0", a.Dropped())
	}
}

func TestAsyncLogger_DropsOnFullBuffer(t *testing.T) {
	// blocked logger never returns from Log, holding the drain goroutine.
	bl := &blockingLogger{started: make(chan struct{})}
	a := NewAsyncLogger(bl, 1, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer a.Close()

	// First call enqueues immediately (buffer has 1 slot); the drain
	// picks it up and blocks inside the inner Log.
	_ = a.Log(context.Background(), Event{Method: "GET", Path: "/1"})
	<-bl.started

	// Second call enqueues into the now-empty buffer.
	_ = a.Log(context.Background(), Event{Method: "GET", Path: "/2"})

	// Third call must drop because buffer is full and drain is still blocked.
	_ = a.Log(context.Background(), Event{Method: "GET", Path: "/3"})

	if a.Dropped() == 0 {
		t.Error("expected at least 1 drop, got 0")
	}
}

func TestAsyncLogger_DelegatesQueryAndCount(t *testing.T) {
	ml := NewMemoryLogger()
	a := NewAsyncLogger(ml, 64, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer a.Close()

	_ = a.Log(context.Background(), Event{Method: "GET", Path: "/a", Status: 200, Success: true})
	// Wait briefly for the drain.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c, _ := a.Count(context.Background(), QueryFilter{}); c == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if c, _ := a.Count(context.Background(), QueryFilter{}); c != 1 {
		t.Errorf("inner count = %d, want 1", c)
	}
	if evs, _ := a.Query(context.Background(), QueryFilter{}); len(evs) != 1 {
		t.Errorf("inner query returned %d, want 1", len(evs))
	}
}

func TestNoopLogger(t *testing.T) {
	n := NoopLogger{}
	if err := n.Log(context.Background(), Event{}); err != nil {
		t.Errorf("Log err: %v", err)
	}
	if evs, _ := n.Query(context.Background(), QueryFilter{}); evs != nil {
		t.Errorf("Query returned %v", evs)
	}
	if c, _ := n.Count(context.Background(), QueryFilter{}); c != 0 {
		t.Errorf("Count = %d", c)
	}
}

type blockingLogger struct {
	started chan struct{}
	once    bool
}

func (b *blockingLogger) Log(ctx context.Context, _ Event) error {
	if !b.once {
		b.once = true
		close(b.started)
	}
	<-ctx.Done()
	return ctx.Err()
}
func (b *blockingLogger) Query(context.Context, QueryFilter) ([]Event, error) { return nil, nil }
func (b *blockingLogger) Count(context.Context, QueryFilter) (int64, error)   { return 0, nil }
