package safe_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"goblast/internal/safe"
)

func TestSafeGoRecovers(t *testing.T) {
	safe.ResetPanicCount()

	var wg sync.WaitGroup
	wg.Add(1)

	safe.GoWithRecovery("test_panic_task", func() {
		panic("simulated panic in background goroutine")
	}, func(r any) {
		wg.Done()
	})

	wg.Wait()

	if safe.PanicCount() != 1 {
		t.Fatalf("Expected PanicCount to be 1, got %d", safe.PanicCount())
	}
}

func TestSafeGoWithRecovery(t *testing.T) {
	safe.ResetPanicCount()

	var wg sync.WaitGroup
	wg.Add(1)

	var recoveredVal any
	safe.GoWithRecovery("test_recovery_callback", func() {
		panic("catastrophic failure")
	}, func(r any) {
		recoveredVal = r
		wg.Done()
	})

	wg.Wait()

	if safe.PanicCount() != 1 {
		t.Fatalf("Expected PanicCount to be 1, got %d", safe.PanicCount())
	}
	if recoveredVal != "catastrophic failure" {
		t.Fatalf("Expected recoveredVal to be 'catastrophic failure', got %v", recoveredVal)
	}
}

func TestSafeLoop(t *testing.T) {
	safe.ResetPanicCount()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var iterCount int
	var mu sync.Mutex

	safe.Loop(ctx, "test_loop", 10*time.Millisecond, func(c context.Context) {
		mu.Lock()
		iterCount++
		count := iterCount
		mu.Unlock()

		if count == 1 {
			panic("panic on first tick")
		}
		if count >= 3 {
			cancel()
		}
	})

	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for loop to finish")
	}

	mu.Lock()
	finalCount := iterCount
	mu.Unlock()

	if finalCount < 3 {
		t.Fatalf("Expected loop to survive panic and execute at least 3 times, got %d", finalCount)
	}
	if safe.PanicCount() != 1 {
		t.Fatalf("Expected PanicCount to be 1, got %d", safe.PanicCount())
	}
}
