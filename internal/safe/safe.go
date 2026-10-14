package safe

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"
)

var panicCounter uint64

// PanicCount mengembalikan total panic yang berhasil ditangkap dan dipulihkan.
func PanicCount() uint64 {
	return atomic.LoadUint64(&panicCounter)
}

// ResetPanicCount mengosongkan penghitung panic (berguna untuk pengujian).
func ResetPanicCount() {
	atomic.StoreUint64(&panicCounter, 0)
}

// IncPanicCount menambah penghitung panic.
func IncPanicCount() uint64 {
	return atomic.AddUint64(&panicCounter, 1)
}

// Go menjalankan fn di goroutine baru dengan penanganan panic otomatis.
func Go(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				IncPanicCount()
				slog.Error("safe.Go: panic dipulihkan",
					"task", name,
					"panic", r,
					"stack", string(debug.Stack()),
				)
			}
		}()
		fn()
	}()
}

// GoWithRecovery menjalankan fn di goroutine baru dengan penanganan panic otomatis
// dan memanggil onPanic jika terjadi panic.
func GoWithRecovery(name string, fn func(), onPanic func(r any)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				IncPanicCount()
				slog.Error("safe.GoWithRecovery: panic dipulihkan",
					"task", name,
					"panic", r,
					"stack", string(debug.Stack()),
				)
				if onPanic != nil {
					defer func() {
						if r2 := recover(); r2 != nil {
							IncPanicCount()
							slog.Error("safe.GoWithRecovery: onPanic callback panic",
								"task", name,
								"panic", r2,
							)
						}
					}()
					onPanic(r)
				}
			}
		}()
		fn()
	}()
}

// Loop menjalankan loop berulang dengan interval tertentu yang berhenti saat ctx.Done().
// Jika terjadi panic di satu iterasi fn, panic dipulihkan dan loop tetap berjalan di iterasi berikutnya.
func Loop(ctx context.Context, name string, interval time.Duration, fn func(ctx context.Context)) {
	Go(name, func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							IncPanicCount()
							slog.Error("safe.Loop: panic dipulihkan",
								"task", name,
								"panic", r,
								"stack", string(debug.Stack()),
							)
						}
					}()
					fn(ctx)
				}()
			}
		}
	})
}

// RunWithRecovery mengeksekusi fn secara synchronous dengan recovery.
func RunWithRecovery(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			IncPanicCount()
			slog.Error("safe.RunWithRecovery: panic dipulihkan",
				"task", name,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()
	fn()
}
