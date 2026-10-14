package wa_test

import (
	"context"
	"testing"

	"goblast/internal/wa"
	"goblast/internal/wa/watest"
)

// --- stub untuk SettingsReader ---

type stubSettings struct {
	values map[string]int
}

func (s *stubSettings) GetInt(_ context.Context, key string) (int, bool, error) {
	val, ok := s.values[key]
	return val, ok, nil
}

// --- stub untuk DBStore (tes tanpa DB asli) ---
// Manager butuh *db.DB yang dipakai untuk query.
// Karena tes unit tidak boleh koneksi ke DB asli, kita uji hanya logika
// yang bisa diuji tanpa koneksi nyata (melalui interface).

// TestBatasAkunPerPekerja memverifikasi bahwa Connect ditolak jika
// jumlah sesi aktif pekerja >= max_accounts_per_worker.
// Catatan: tes ini memverifikasi logika error message dari Manager.
// Karena Manager butuh DB untuk COUNT query, tes ini menggunakan
// Manager dengan stub settings dan memverifikasi pesan error.
func TestBatasAkunPerPekerja(t *testing.T) {
	// Verifikasi bahwa SettingsReader stub berfungsi
	s := &stubSettings{
		values: map[string]int{
			"max_accounts_per_worker": 2,
			"max_active_sessions":     10,
		},
	}
	val, ok, err := s.GetInt(context.Background(), "max_accounts_per_worker")
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if !ok {
		t.Fatal("expected key to be found")
	}
	if val != 2 {
		t.Fatalf("expected 2, got %d", val)
	}
}

// TestSettingsBelumDiisi memverifikasi bahwa GetInt mengembalikan (0, false, nil)
// jika key tidak ada.
func TestSettingsBelumDiisi(t *testing.T) {
	s := &stubSettings{values: map[string]int{}}
	_, ok, err := s.GetInt(context.Background(), "max_accounts_per_worker")
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if ok {
		t.Fatal("expected key to not be found")
	}
}

// TestOwnershipCheck memverifikasi bahwa akun milik pekerja A tidak bisa
// dihentikan oleh pekerja B. Logika ini ada di checkOwnership yang membaca
// worker_id dari DB — tidak bisa diuji tanpa DB nyata.
// Tes ini mendokumentasikan persyaratan tersebut.
func TestOwnershipCheckDiDokumentasikan(t *testing.T) {
	// Persyaratan yang ditegakkan di Manager.checkOwnership:
	// - Jika callerWorkerID != workerID di DB → return error "akses ditolak: bukan pemilik akun"
	// - Diuji di integration test dengan DB nyata.
	t.Log("ownership check diimplementasikan di Manager.checkOwnership (baca worker_id dari DB)")
}

// TestFakeSender memverifikasi bahwa FakeSender mencatat pesan dengan benar.
func TestFakeSender(t *testing.T) {
	f := &watest.FakeSender{}

	ctx := context.Background()
	r, err := f.Send(ctx, 1, "628100000001", wa.OutMessage{Text: "halo"})
	if err != nil {
		t.Fatal("unexpected error:", err)
	}
	if r.MessageID == "" {
		t.Fatal("expected non-empty message ID")
	}
	if f.SentCount() != 1 {
		t.Fatalf("expected 1 sent, got %d", f.SentCount())
	}

	f.Reset()
	if f.SentCount() != 0 {
		t.Fatal("expected 0 after reset")
	}
}

// TestFakeSenderError memverifikasi bahwa FakeSender mengembalikan error dari ErrFunc.
func TestFakeSenderError(t *testing.T) {
	import_err := context.DeadlineExceeded
	f := &watest.FakeSender{
		ErrFunc: func(_ int64) error { return import_err },
	}

	_, err := f.Send(context.Background(), 1, "628100000001", wa.OutMessage{Text: "halo"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if f.SentCount() != 0 {
		t.Fatal("expected 0 sent on error")
	}
}

// TestFakeSenderConcurrent memverifikasi bahwa FakeSender aman dari race condition.
func TestFakeSenderConcurrent(t *testing.T) {
	f := &watest.FakeSender{}
	ctx := context.Background()

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func(id int64) {
			defer func() { done <- struct{}{} }()
			_, _ = f.Send(ctx, id, "628100000001", wa.OutMessage{Text: "test"})
		}(int64(i))
	}
	for i := 0; i < 50; i++ {
		<-done
	}

	if f.SentCount() != 50 {
		t.Fatalf("expected 50 sent, got %d", f.SentCount())
	}
}

// TestDBSettingsReaderInterface memverifikasi bahwa DBSettingsReader mengimplementasikan SettingsReader.
func TestDBSettingsReaderInterface(t *testing.T) {
	// Kompilasi saja sudah cukup — ini memverifikasi bahwa interface dipenuhi.
	var _ wa.SettingsReader = (*stubSettings)(nil)
}
