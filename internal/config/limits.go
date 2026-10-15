package config

import "time"

// MinConvertIDR adalah jumlah minimum rupiah yang dapat dikonversi ke USDT.
const MinConvertIDR int64 = 10000

// KursMaxAgeHours adalah umur maksimum kurs rate_idr_to_usdt dalam jam sebelum dianggap basi/kedaluwarsa.
const KursMaxAgeHours = 24

// MinWithdrawIDR adalah jumlah minimal penarikan dalam rupiah (usulan 10000).
const MinWithdrawIDR int64 = 10000

// MinWithdrawUSDTMicro adalah jumlah minimal penarikan USDT dalam mikro-USDT (5 USDT = 5.000.000 mikro-USDT).
const MinWithdrawUSDTMicro int64 = 5000000

// MaxPendingWithdrawals adalah jumlah maksimum penarikan berstatus 'menunggu' yang diizinkan per pekerja.
const MaxPendingWithdrawals = 1

// WithdrawLockAfterChangeHours adalah durasi penahanan penarikan (dalam jam) setelah pergantian rekening atau reset kata sandi.
const WithdrawLockAfterChangeHours = 24

// SpeedDelays mendefinisikan jeda antar pengiriman per profil kecepatan:
// fast 1 detik, medium 5 detik, slow 15 detik, over_slow 30 detik.
// Dideklarasikan sebagai variabel paket agar perilakunya dapat disesuaikan pada pengujian.
var SpeedDelays = map[string]time.Duration{
	"fast":      1 * time.Second,
	"medium":    5 * time.Second,
	"slow":      15 * time.Second,
	"over_slow": 30 * time.Second,
}

var (
	// JitterPercent adalah persentase variasi acak jeda kirim (±20%).
	JitterPercent = 20

	// MaxAttempts adalah batas maksimal percobaan pengiriman per nomor sebelum berstatus 'gagal'.
	MaxAttempts = 3

	// MaxConsecutiveFailures adalah batas kegagalan berturut-turut sebelum akun dihentikan otomatis.
	MaxConsecutiveFailures = 5

	// ClaimTimeoutSeconds adalah batas kedaluwarsa klaim nomor berstatus 'dikirim' (300 detik).
	ClaimTimeoutSeconds = 300

	// SendTimeoutSeconds adalah batas waktu pengiriman pesan ke WhatsApp (60 detik).
	SendTimeoutSeconds = 60

	// DefaultCountryCode adalah kode negara bawaan untuk normalisasi nomor telepon ("62").
	DefaultCountryCode = "62"

	// DailyLimitPerAccount adalah batas pesan per hari per akun (0 berarti tanpa batas, keputusan pemilik).
	DailyLimitPerAccount = 0

	// DefaultDBMaxConns adalah batas maksimal koneksi pool database (Tahap H1).
	DefaultDBMaxConns = 20

	// DefaultDBMinConns adalah batas minimal koneksi pool database (Tahap H1).
	DefaultDBMinConns = 2

	// DefaultDBMaxConnLifetime adalah batas umur maksimal koneksi pool (30 menit).
	DefaultDBMaxConnLifetime = 30 * time.Minute

	// DefaultDBMaxConnIdleTime adalah batas idle maksimal koneksi pool (5 menit).
	DefaultDBMaxConnIdleTime = 5 * time.Minute

	// DefaultDBHealthCheckPeriod adalah periode health check koneksi pool (30 detik).
	DefaultDBHealthCheckPeriod = 30 * time.Second

	// DefaultDBConnectTimeout adalah batas waktu timeout saat membuat koneksi (5 detik).
	DefaultDBConnectTimeout = 5 * time.Second

	// DefaultDBStatementTimeout adalah statement_timeout PostgreSQL (30 detik).
	DefaultDBStatementTimeout = "30s"

	// DefaultDBLockTimeout adalah lock_timeout PostgreSQL (5 detik).
	DefaultDBLockTimeout = "5s"

	// DefaultDBIdleInTransactionTimeout adalah idle_in_transaction_session_timeout PostgreSQL (30 detik).
	DefaultDBIdleInTransactionTimeout = "30s"

	// DefaultWhatsmeowMaxOpenConns adalah batas maksimal open connection pool whatsmeow.
	DefaultWhatsmeowMaxOpenConns = 10

	// DefaultWhatsmeowMaxIdleConns adalah batas maksimal idle connection pool whatsmeow.
	DefaultWhatsmeowMaxIdleConns = 2

	// DefaultWhatsmeowConnMaxLifetime adalah umur maksimal koneksi pool whatsmeow (30 menit).
	DefaultWhatsmeowConnMaxLifetime = 30 * time.Minute

	// DisconnectTimeout adalah batas waktu putus sementara sebelum sesi dihentikan (5 menit).
	DisconnectTimeout = 5 * time.Minute

	// PairingTimeout adalah batas waktu operasi pairing phone whatsmeow (30 detik).
	PairingTimeout = 30 * time.Second

	// DrainAccountTimeout adalah batas waktu tunggu drain worker akun saat Stop atau Delete (10 detik).
	DrainAccountTimeout = 10 * time.Second

	// --- Konstanta Keamanan Akses Tahap H5a ---

	// MaxPasswordBytes batas panjang maksimal kata sandi sebelum di-hash (128 byte).
	MaxPasswordBytes = 128

	// ArgonConcurrencyLimit batas hashing argon2 yang berjalan bersamaan (2).
	ArgonConcurrencyLimit = 2

	// ArgonQueueLimit batas antrean pemohon hashing argon2 (8).
	ArgonQueueLimit = 8

	// ArgonMaxWait batas maksimal tunggu antrean hashing argon2 (2 detik).
	ArgonMaxWait = 2 * time.Second

	// LoginRateLimitIPAttempts batas gagal login per IP (20 per 15 menit).
	LoginRateLimitIPAttempts = 20
	// LoginRateLimitIPWindow jendela waktu batas gagal login per IP (15 menit).
	LoginRateLimitIPWindow = 15 * time.Minute

	// LoginRateLimitIdentifierAttempts batas gagal login per identifier (10 per 15 menit).
	LoginRateLimitIdentifierAttempts = 10
	// LoginRateLimitIdentifierWindow jendela waktu batas gagal login per identifier (15 menit).
	LoginRateLimitIdentifierWindow = 15 * time.Minute

	// LoginRateLimitPairAttempts batas gagal login per pasangan IP+identifier (5 per 15 menit).
	LoginRateLimitPairAttempts = 5
	// LoginRateLimitPairWindow jendela waktu batas gagal login per pasangan IP+identifier (15 menit).
	LoginRateLimitPairWindow = 15 * time.Minute

	// LoginRateLimitGlobalAttempts batas gagal login global per menit (300).
	LoginRateLimitGlobalAttempts = 300
	// LoginRateLimitGlobalWindow jendela waktu batas gagal login global (1 menit).
	LoginRateLimitGlobalWindow = 1 * time.Minute

	// RegisterRateLimitHourlyAttempts batas pendaftaran per IP per jam (10).
	RegisterRateLimitHourlyAttempts = 10
	// RegisterRateLimitHourlyWindow jendela waktu pendaftaran per jam (1 jam).
	RegisterRateLimitHourlyWindow = 1 * time.Hour

	// RegisterRateLimitDailyAttempts batas pendaftaran per IP per hari (100).
	RegisterRateLimitDailyAttempts = 100
	// RegisterRateLimitDailyWindow jendela waktu pendaftaran per hari (24 jam).
	RegisterRateLimitDailyWindow = 24 * time.Hour

	// MaxLimiterKeys batas jumlah entri dalam map rate limiter (LRU eviction).
	MaxLimiterKeys = 10000

	// MaxUserWebSessions batas sesi web aktif per pengguna (5).
	MaxUserWebSessions = 5

	// AuditLoginFailedRateLimitAttempts batas penulisan log audit login_failed per IP per menit (20).
	AuditLoginFailedRateLimitAttempts = 20
	// AuditLoginFailedRateLimitWindow jendela waktu batas audit login_failed (1 menit).
	AuditLoginFailedRateLimitWindow = 1 * time.Minute
)

