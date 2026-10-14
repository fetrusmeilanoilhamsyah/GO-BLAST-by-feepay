package engine

import (
	"context"
	"errors"
	"net"
	"strings"

	"goblast/internal/wa"
	"go.mau.fi/whatsmeow"
)

// ResultClass mengklasifikasikan hasil pengiriman pesan WhatsApp.
type ResultClass int

const (
	// ClassTerkirim pesan berhasil terkirim dan diakui server.
	ClassTerkirim ResultClass = iota

	// ClassPastiBelumTerkirim sesi tidak aktif atau terputus sebelum permintaan ditulis.
	// Aman untuk dikembalikan ke tersedia dan rollback attempts.
	ClassPastiBelumTerkirim

	// ClassTidakPasti timeout, koneksi putus saat menunggu ack, context dibatalkan,
	// atau error tak dikenal. Diberi penanganan aman terhadap pesan ganda (A1: gagal).
	ClassTidakPasti

	// ClassNomorTidakValid nomor tidak terdaftar atau server menolak JID / format nomor.
	ClassNomorTidakValid

	// ClassMasalahAkun akun mengalami logout, blokir/ban, atau pembatasan laju dari WhatsApp.
	ClassMasalahAkun
)

func (r ResultClass) String() string {
	switch r {
	case ClassTerkirim:
		return "terkirim"
	case ClassPastiBelumTerkirim:
		return "pasti_belum_terkirim"
	case ClassTidakPasti:
		return "tidak_pasti"
	case ClassNomorTidakValid:
		return "nomor_tidak_valid"
	case ClassMasalahAkun:
		return "masalah_akun"
	default:
		return "unknown"
	}
}

// Classify mengklasifikasikan error kirim menjadi ResultClass yang terdefinisi.
// Error tak dikenal diperlakukan sebagai ClassTidakPasti (aman terhadap pesan ganda).
func Classify(err error) ResultClass {
	if err == nil {
		return ClassTerkirim
	}

	// 1. Sesi tidak aktif atau terputus sebelum kirim dimulai
	if errors.Is(err, wa.ErrSessionNotActive) ||
		errors.Is(err, wa.ErrSessionNotConnected) ||
		errors.Is(err, whatsmeow.ErrClientIsNil) ||
		errors.Is(err, whatsmeow.ErrNotConnected) ||
		errors.Is(err, whatsmeow.ErrNotLoggedIn) ||
		errors.Is(err, whatsmeow.ErrAlreadyConnected) {
		return ClassPastiBelumTerkirim
	}

	// 2. Format nomor / JID tidak valid
	if errors.Is(err, whatsmeow.ErrRecipientADJID) ||
		errors.Is(err, whatsmeow.ErrUnknownServer) ||
		errors.Is(err, whatsmeow.ErrInvalidInlineBotID) ||
		errors.Is(err, whatsmeow.ErrPhoneNumberTooShort) ||
		errors.Is(err, whatsmeow.ErrPhoneNumberIsNotInternational) ||
		errors.Is(err, whatsmeow.ErrIQBadRequest) ||
		errors.Is(err, whatsmeow.ErrIQNotFound) {
		return ClassNomorTidakValid
	}

	// 3. Masalah akun dari server WhatsApp
	if errors.Is(err, whatsmeow.ErrIQNotAuthorized) ||
		errors.Is(err, whatsmeow.ErrIQForbidden) ||
		errors.Is(err, whatsmeow.ErrIQResourceLimit) ||
		errors.Is(err, whatsmeow.ErrIQLocked) ||
		errors.Is(err, whatsmeow.ErrIQRateOverLimit) {
		return ClassMasalahAkun
	}

	// Cek IQError terstruktur
	var iqErr *whatsmeow.IQError
	if errors.As(err, &iqErr) {
		switch iqErr.Code {
		case 400, 404:
			return ClassNomorTidakValid
		case 401, 403, 419, 423, 429:
			return ClassMasalahAkun
		}
	}

	// Cek ErrServerReturnedError dengan kode error numerik
	if errors.Is(err, whatsmeow.ErrServerReturnedError) {
		msg := err.Error()
		if strings.Contains(msg, "400") || strings.Contains(msg, "404") {
			return ClassNomorTidakValid
		}
		if strings.Contains(msg, "401") || strings.Contains(msg, "403") ||
			strings.Contains(msg, "419") || strings.Contains(msg, "423") ||
			strings.Contains(msg, "429") {
			return ClassMasalahAkun
		}
	}

	// 4. Timeout atau terputus saat menunggu konfirmasi / respons
	var discErr *whatsmeow.DisconnectedError
	if errors.As(err, &discErr) ||
		errors.Is(err, whatsmeow.ErrMessageTimedOut) ||
		errors.Is(err, whatsmeow.ErrIQTimedOut) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return ClassTidakPasti
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ClassTidakPasti
	}

	// Error tidak dikenal: perlakuan default aman tidak pasti
	return ClassTidakPasti
}
