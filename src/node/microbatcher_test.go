package main

import (
	"sync"
	"testing"
	"time"

	"github.com/qdrant/go-client/qdrant"
)

func TestMicroBatcher_FlushesAfterWindow(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]*qdrant.PointStruct

	mb := &microBatcher{
		window: 50 * time.Millisecond,
		flushFn: func(pts []*qdrant.PointStruct) {
			mu.Lock()
			flushed = append(flushed, pts)
			mu.Unlock()
		},
	}

	pt := &qdrant.PointStruct{}
	mb.add([]*qdrant.PointStruct{pt})

	time.Sleep(150 * time.Millisecond) // wait > window

	mu.Lock()
	count := len(flushed)
	mu.Unlock()

	if count == 0 {
		t.Error("expected flush to have fired, but flushed is empty")
	}
}

func TestMicroBatcher_NoFlushWhenEmpty(t *testing.T) {
	called := false
	mb := &microBatcher{
		window: 20 * time.Millisecond,
		flushFn: func(pts []*qdrant.PointStruct) {
			called = true
		},
	}
	// Don't add anything.
	time.Sleep(60 * time.Millisecond)
	if called {
		t.Error("flushFn should not be called for an empty batcher")
	}
	_ = mb
}
