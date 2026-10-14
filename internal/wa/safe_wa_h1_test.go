package wa_test

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
	"goblast/internal/safe"
)

// TestSafeEventHandlerRecovery membuktikan bahwa kepanikan dalam event callback whatsmeow
// ditangkap oleh safe.RunWithRecovery sehingga proses tidak mati dan safe.PanicCount() bertambah.
func TestSafeEventHandlerRecovery(t *testing.T) {
	safe.ResetPanicCount()

	initialPanics := safe.PanicCount()

	// Simulasi event handler callback yang memanggil safe.RunWithRecovery
	handler := func(evt interface{}) {
		safe.RunWithRecovery("wa.eventHandler-simulasi", func() {
			if _, ok := evt.(*events.LoggedOut); ok {
				panic("simulasi panic dalam pemrosesan event whatsmeow")
			}
		})
	}

	// Picu event yang menyebabkan panic
	handler(&events.LoggedOut{Reason: events.ConnectFailureGeneric})

	if safe.PanicCount() != initialPanics+1 {
		t.Fatalf("Ekspektasi safe.PanicCount bertambah 1, didapat %d", safe.PanicCount())
	}
}

// TestSafeQRReaderPanicRecovery membuktikan bahwa goroutine pembaca QR yang mengalami panic
// ditangkap oleh safe.Go sehingga goroutine lain dan proses utama tidak mati.
func TestSafeQRReaderPanicRecovery(t *testing.T) {
	safe.ResetPanicCount()

	initialPanics := safe.PanicCount()
	done := make(chan struct{})

	safe.Go("wa.qrReader-simulasi", func() {
		defer close(done)
		panic("simulasi panic saat memproses QR code image")
	})

	select {
	case <-done:
		// Beri sedikit waktu untuk recover handler mencatat panic
		time.Sleep(50 * time.Millisecond)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout menunggu goroutine selesai")
	}

	if safe.PanicCount() != initialPanics+1 {
		t.Fatalf("Ekspektasi safe.PanicCount bertambah 1, didapat %d", safe.PanicCount())
	}
}
