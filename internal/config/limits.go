package config

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
