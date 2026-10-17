# ARSIP_PROGRES: GO BLAST

Arsip log eksekusi dan catatan temuan historis untuk Tahap 0 sampai Tahap H10.
Dokumen aktif dan log terkini (Tahap H11 ke atas) berada di [DOC_PROGRES.md](DOC_PROGRES.md).

---

## Catatan Temuan Historis

### Rangkaian Tahap H (H1 – H10)

- 2026-10-18 (Tahap H10 — Uji Beban dan Kekacauan, lalu Penyetelan Angka):
  1. Harness Pengujian Aman (`internal/loadtest`): Dibangun dengan build tag `load` terisolasi sehingga tidak ikut dalam `go test ./...` biasa; diproteksi `RequireSafety` yang menolak jalan jika `APP_ENV=production` atau nama database tidak berakhiran `_test`; nomor dibangkitkan secara acak fiktif (`628100...`) tanpa melibatkan koneksi WhatsApp fisik nyata (menggunakan `FakeSender` dengan injeksi latensi dan error).
  2. Hasil Uji Empiris 11 Skenario (L1–L11):
     - L1 (Beban Stabil 50 worker): Terkirim 2.671 pesan, Klaim 2.671, Gagal 0, 0 duplikat, Upah cocok 100% (Rp 133.550). RT DB/pesan = 2.121 (<= 2.5). p95 klaim < 10ms (96.71% < 10ms). Pool: PeakAcquired=25/25, EmptyAcquires=3.25%, AvgWait=23.74ms.
     - L2 (Serentak Thundering Herd): 50/50 akun berhasil mengklaim nomor dalam 399.8 ms (< 5 detik). 0 error koneksi pool, 0 deadlock.
     - L3 (DB Lambat): Latensi 50ms disuntikkan acak ke 10% kueri database. Terkirim 662, Klaim 662, 0 duplikat, 0 nomor yatim. Engine pulih mandiri. Latensi `/readyz` p95 < 1ms (mean 13.15µs, max 855µs <= 100ms).
     - L4 (WA Bermasalah): Outage WhatsApp 60 detik dengan penolakan ErrNotConnected. 306 penolakan tercatat, 306 backoff log, 0 nomor gugur permanen (rollback ke 'tersedia'). Pulih mengirim lancar (145 pesan) setelah koneksi normal. Memperbaiki bug circuit breaker mesin kirim.
     - L5 (Mati Mendadak & Crash A1): Goroutine crash mendadak (`runtime.Goexit`) saat pesan in-flight. 7 nomor in-flight saat crash, `RecoverOnBoot` memulihkan tepat 7 nomor ke status 'gagal' dengan `last_error='hasil_tidak_pasti'`. 0 nomor 'dikirim' tertinggal, 0 duplikat.
     - L6 (HTTP Load & Brute Force): 1.000 sesi rate limit + 200 req/s brute force login & polling. Puncak memori 203.81 MB (kenaikan 195.16 MB <= 300 MB). `/healthz` p95: 521.9µs, `/readyz` p95: 777.3µs. Login & poll: 429=9.481, 200=399, 0 timeouts, 0 panic.
     - L7 (Shutdown under Load): SIGTERM saat 50 worker aktif mengirim. Shutdown selesai dalam 46.63 ms (< 25s), 0 nomor menggantung, 96/96 nomor terkirim memiliki buku besar valid (Rp 4.800), 0 upah hilang.
     - L8 (Unggah 100k): ConfirmUpload 100.000 nomor via COPY CSV selesai dalam 4.73 detik! Memori delta: 18.58 MB. 100k nomor tersimpan akurat di database.
     - L9 (Kueri 1M Nomor & 500k Buku Besar): Pengujian 6 kueri kritis pada dataset 1.000.000 nomor: 1. Klaim (SKIP LOCKED): 1.017 ms; 2. Supervisor: 241.83 ms; 3. Pembersih Stale: 0.039 ms; 4. Dashboard Group: 142.88 ms; 5. Saldo 1 Pekerja (500k baris): 126.81 ms (> 50 ms -> usul tabel ringkasan saldo); 6. Pendapatan Hari Ini: 173.63 ms.
     - L10 (Backup & Restore Besar): Backup & restore 1.510.067 baris & file uploads. Backup: 1.94 detik (file 6.97 MB), Heap delta: 7.89 MB (<< 150 MB), `/healthz` p95: 0s (< 1ms). Restore: 52.07 detik (16 tabel), seluruh 5 invarian atomik valid 100%.
     - L11 (Purge Campaign 200k & VACUUM): Purge 200k nomor & VACUUM ANALYZE saat beban ringan 5 worker. Purge selesai dalam 4.37 detik (50k tersedia dihapus fisik, 100k terkirim, 25k gagal, 25k tidak_terdaftar dianonimkan), VACUUM 760.49 ms, puncak blokir kueri 17.82 ms (<< 2s), 0 nomor tersedia tersisa, integritas FK buku besar 1.000 baris upah utuh 100%.
  3. Perbaikan Temuan Kode & Database:
     - Perbaikan Bug Pemutus Sirkuit Engine (`internal/engine/engine.go`): Error jaringan transien whatsmeow berkelas `ClassPastiBelumTerkirim` sebelumnya salah menaikkan `consecutiveFailures`, menyebabkan akun terhenti permanen setelah 5 kali putus sementara. Diperbaiki dengan mencatat `BackoffCount`, jeda acak jitter 2 detik, dan melewati kenaikan circuit breaker.
     - Migrasi 0024 (`db/migrations/0024_ledger_campaign_number_fk_index.sql`): Menambahkan indeks tanpa kondisi `idx_ledger_entries_campaign_number_id ON ledger_entries (campaign_number_id)` untuk mempercepat penegakan foreign key ON DELETE RESTRICT saat penghapusan nomor kampanye massal (Purge). Uji siklus migrasi Goose: Up 24 -> DownTo 18 -> Up 24 terverifikasi lulus 100%.
     - Perbaikan Tes Sesi Web (`internal/web/referral_h6_test.go`): Mengganti modulo random kecil pada pembuatan username/referral mock dengan penanganan error eksplisit dan timestamp unix nano unik.
  4. Rekomendasi Penyetelan Parameter (Belum Diukur dengan Sesi WhatsApp Nyata):
     - `DB_MAX_CONNS`: 25–30 (cukup untuk 50 worker serentak; saat beban puncak 50 worker fast, pool peak acquired 25/25, antrean tunggu 3.25% dengan rerata tunggu 23.74ms).
     - `DB_MIN_CONNS`: 5 (menjaga pool tetap hangat saat proses booting).
     - Batas koneksi whatsmeow: 10 (`SetMaxOpenConns(10)`, `SetMaxIdleConns(2)`).
     - `MemoryMax` systemd: disetel sebesar 75% RAM VPS sebagai aturan pengaman (angka tetap 512 MB sebelumnya hanya contoh; puncak riil L6 adalah 203.81 MB pada 1.000 sesi rate limit + 200 req/s brute force, namun berjalan **tanpa sesi WhatsApp**).
     - Rumus `MAX_ACTIVE_SESSIONS`: `MAX_ACTIVE_SESSIONS = ⌊(memori_aplikasi - memori_dasar) / (1.5 × memori_per_sesi)⌋`. Memori per sesi whatsmeow **belum pernah diukur dengan sesi WhatsApp nyata** (estimasi 3 MB atau 4.5 MB sebelumnya hanyalah asumsi teoretis). Angka 75–80 sesi belum terbukti; sementara dipasang nilai awal konservatif `MAX_ACTIVE_SESSIONS = 20` sampai pemilik melakukan pengukuran riil.
     - Rancangan Tabel Ringkasan Saldo (Kueri 5 L9: 126.81 ms > 50 ms): Diusulkan tabel ringkasan saldo `user_balances (user_id BIGINT PRIMARY KEY, currency VARCHAR(10), balance BIGINT, updated_at TIMESTAMPTZ)` yang di-update secara atomik saat penulisan `ledger_entries` untuk menghindari Seq Scan penuh pada 500k+ baris buku besar.
  5. Verifikasi Pengujian: Seluruh pengujian paket repo standar lulus 100% (`go test -p 1 ./...`) dan 11 skenario uji beban lulus 100% (`go test -tags load ./internal/loadtest`).

- 2026-10-17 (Tahap H9 — Backup Aman dan Pemulihan):
  1. Format Arsip Cadangan `.gbak`: Format stream terenkripsi AES-256-GCM 64 KB + gzip dengan unencrypted JSON header (`GOBLAST1`) dan trailer integritas (`GBAKEND1`).
  2. Eksklusi Selektif & Rotasi Kunci: Eksklusi ketat tabel `whatsmeow_*` (kunci perangkat WA tidak boleh disalin), `web_sessions`, dan `goose_db_version`. `BACKUP_KEY` (32 byte base64) terpisah dari `ENCRYPTION_KEY` dengan rotasi via `BACKUP_KEY_PREV`.
  3. Penjadwalan & Retensi GFS: Penjadwalan otomatis harian 03:00 WIB via `safe.Go`. Retensi GFS (7 harian, 4 mingguan) dengan garansi keras tidak pernah menghapus cadangan terverifikasi terakhir.
  4. Pemulihan Atomik CLI (`goblast restore`): 1 transaksi atomik dengan nonaktifkan trigger ledger, truncate berurutan dependensi terbalik, streaming COPY FROM, reset `wa_accounts` ke 'terputus', reset sequences, 5 cek invarian ketat, ekstraksi `uploads/`, dan uji dekripsi `ENCRYPTION_KEY`.
  5. Panel Admin & Pengamanan Web: Panel `/admin/backups` (desktop tabel & mobile kartu) dengan Step-Up TOTP dan streaming download. Pemulihan via web mati bawaan (`ALLOW_WEB_RESTORE=false`). Baris pemantauan status cadangan di `/admin/maintenance` dan metrik API. Seluruh tes lulus 100%.

- 2026-10-17 (Tahap H8 — Arsip yang Jelas, Retensi Data, dan Pembersihan Server):
  1. Pengujian Temuan P5 dan P7 (Aturan 1 — Tulis tes gagal sebelum memperbaiki):
     - P5 (Retensi & Pembersihan Unggahan Sementara `data/tmp/`): `TestH8_Flaw_ConfirmUploadLeavesRejectedCSV` membuktikan bahwa sebelum H8 alur konfirmasi upload nomor meninggalkan file rejected CSV dan meta chunk di `data/tmp/` tanpa dibersihkan. Diperbaiki: `ConfirmUpload` dan `CancelUpload` kini membersihkan semua berkas sementara secara atomik. `TestH8_CancelUploadRemovesAllFiles` dan `TestH8_CleanTmpFiles1Hour` memverifikasi pembersihan seluruh berkas chunk dan file usang > 1 jam (`TmpFilesMaxAge = 1 hour`).
     - P7 & Keputusan O (Purge Kampanye & Anonimisasi Customer): `TestPurge_AnonymizeCustomerData` membuktikan bahwa aksi purge tidak merusak integritas finansial FK `ledger_entries` (ON DELETE RESTRICT). Baris nomor `terkirim`, `gagal`, dan `tidak_terdaftar` tetap tersimpan tetapi dianonimkan (`phone_e164 = 'x' || id`, `country_code = 'XX'`, `recipient_name = NULL`, `custom_data = NULL`). Kolom `last_error` disanitasi: nilai whitelist (`hasil_tidak_pasti`, `terkirim_tidak_tercatat`, `galat_internal`) dipertahankan; selain itu diubah ke `'lainnya'`; pada nomor terkirim dan tidak_terdaftar dikosongkan (`NULL`). Hanya nomor `tersedia` yang dihapus fisik. Kolom `number_batches.filename` dikosongkan dan `campaigns.purge_summary` menyimpan snapshot statistik.
     - Penolakan Purge pada Kampanye Berjalan/In-Flight: `TestPurge_RunningAndInFlightRejected` membuktikan purge ditolak jika status kampanye `berjalan` atau memiliki nomor `in_flight` (status `'dikirim'`).
     - Idempotensi & Pembersihan Gambar: `TestPurge_Idempotent` membuktikan pemanggilan purge berulang aman (`AlreadyPurged`). `TestPurge_ImageDeletion` membuktikan opsi hapus gambar template di `uploads/` hanya menghapus jika berkas tidak dirujuk oleh kampanye lain.
  2. Pemeliharaan, Penyimpanan Server, dan Pengingat 90 Hari:
     - Halaman Baru `/admin/storage`: `TestH8_Flaw_StoragePage404` membuktikan rute baru terdaftar dan menampilkan ukuran DB (`pg_database_size`), ukuran 4 tabel utama (`pg_total_relation_size`), ukuran 3 folder (`uploads/`, `data/tmp/`, `backups/`), daftar kampanye terarsip & selesai, serta 4 kartu aksi pembersihan mandiri.
     - Pengingat Kampanye Terarsip > 90 Hari: `TestH8_Flaw_MaintenanceArchivedReminder` membuktikan health check ke-10 di `/admin/maintenance` (`archived_campaigns_retention`) memicu status 'warning' jika ada kampanye terarsip yang belum di-purge setelah 90 hari (`ArchivedPurgeReminderDays`).
     - Pembersihan Retensi Sistem: Menguji pembersihan sesi web kedaluwarsa, log audit kegagalan autentikasi > 30 hari (`RetentionAuditDays`), dan notifikasi terbaca > 90 hari (`RetentionNotificationDays`).
     - Database Vacuum: Menguji eksekusi mandiri `VACUUM ANALYZE` pada koneksi tersendiri dengan batas `SET lock_timeout = '5s'`.
     - Rate Limiting Storage: Menguji pembatasan laju maksimal 5 kali/jam untuk aksi pembersihan storage dan purge kampanye.
  3. Kepatuhan Pengawal Arsitektur (Guards):
     - `TestTersediaQueryGuard`: Seluruh query yang menyentuh `status = 'tersedia'` untuk penghapusan nomor purge (`DeleteAvailableCampaignNumbers`) dan kalkulasi statistik storage (`GetStorageCampaignStats`) ditempatkan di `internal/campaign/claimable.go`.
     - `TestNoHardcodedColorsInTemplates`: Backdrop modal purge menggunakan variabel CSS `--color-modal-backdrop` tanpa warna rgba hardcoded.
     - `TestGuard_SQL_NoManualConcat`: Eksekusi `VACUUM ANALYZE` di `admin_storage.go` menggunakan string literal statis eksplisit per tabel tanpa konkatenasi operator `+`.
  4. Pengujian Penuh:
     - Seluruh tes di seluruh paket repositori (`go test -p 1 ./...`) lulus 100%.

- 2026-10-15 (Tahap H7 — Format Pesan dan Siklus Campaign):
  1. Pengujian Temuan P1 sampai P4 (Aturan 1 — Tulis tes gagal sebelum memperbaiki):
     - P1 (Pratinjau Foto): `TestH7_Flaw_PreviewWithoutSave` membuktikan bahwa pratinjau sebelumnya mengandalkan `existing_image_path` yang rentan path traversal dan menulis file sementara ke disk. Diperbaiki: pratinjau membaca multipart langsung ke data URI base64 di memori dan render kartu_link memakai `GenerateThumbnailFromBytes` (ThumbnailSpec 300px kualitas 70). Edit campaign membaca `image_path` resmi dari DB berdasarkan ID.
     - P2b (Variabel Engine vs Pratinjau): `TestH7_Flaw_EngineFillsRecipientVariables` membuktikan bahwa `buildCampaignMessage` sebelumnya mengabaikan `custom_data` nomor penerima dan mengirimkan raw template. Diperbaiki: `ClaimNextNumber` memuat `custom_data`, dan `buildCampaignMessage` memanggil `campaign.RenderForRecipient(tpl, &rec)`.
     - P2c (Variabel Sanitasi & Aliases): `TestH7_Finding_VariableAliasesAndSanitization` membuktikan alias (`nama`/`name`, `usn`/`username`, `id`) kini dipetakan konsisten, nilai kustom dibersihkan dari karakter kontrol, baris baru, karakter format WA (`*`, `_`, `~`, ```), dan dipotong maks 60 rune.
     - P2d (Bug Fallback Variabel Kosong): `TestH7_Finding_EmptyVariablesFallback` membuktikan bahwa sebelum H7, jika map variabel kosong sama sekali, regex penggantian dilewati sehingga `{nama|Kak}` tetap mentah sebagai `{nama|Kak}`. Diperbaiki sehingga `{nama|Kak}` berubah menjadi fallback `"Kak"`.
     - P3a (Opsi Tombol Asli & Batas 20 Penerima): `TestH7_Finding_NativeFlowMessageBuilderAndLimits` memverifikasi `MessageBuilder` membangun `NativeFlowMessage` (bukan `TemplateMessage` yang telah mati), kuota upload/start menolak >20 nomor jika mode `tombol_asli`, dan form menolak submit tanpa centang persetujuan eksperimental.
     - P4a & P4b (Dijeda & Ekspor Sisa Nomor): `TestH7_Flaw_PausedCampaignStatusAndExport` membuktikan campaign draf dengan `started_at` kini berstatus "Dijeda", dan endpoint `/admin/campaigns/{id}/numbers/export` hanya mengekspor nomor berstatus `tersedia` (Keputusan R) dalam CSV berformat aman anti formula injection, serta menolak campaign yang sedang `berjalan`.
  2. Proteksi Query Tersedia (`TestTersediaQueryGuard`): Seluruh query nomor berstatus `'tersedia'` untuk ekspor, pengecekan variabel, dan daftar custom keys dipusatkan di `internal/campaign/claimable.go` (`QueryAvailableExportNumbers`, `HasAvailableVariableData`, `GetAvailableCustomKeys`).
  3. Sanitasi dan Keamanan Gambar: `ProcessAndSaveCampaignImage` memvalidasi magic bytes header (JPEG: `\xFF\xD8\xFF`, PNG: `\x89PNG\r\n\x1a\n`), memeriksa batas dimensi maksimal 4096px, men-decode gambar lalu meng-encode ulang untuk melucuti EXIF/GPS sensitif, dan menyimpannya dengan nama UUID acak.
  4. Template URL Auto-Escaping (`#ZgotmplZ`): `html/template` Go secara otomatis meng-escape `data:` URI di `<img src="...">` jika berupa string mentah. Diselesaikan dengan memberikan tipe `template.URL` eksplisit pada struct template.
  5. Seluruh tes paket repositori lulus 100%.

- 2026-10-15 (Tahap H6 & 18 — Referral Otomatis & Anti-Kecurangan):
  1. Engine pembayaran referral otomatis (Temuan B2 & Keputusan D): Diimplementasikan ke dalam transaksi atomik pengiriman pesan sukses (`RecordSuccessAtomic` / CTE tunggal satu round trip ke database). Membayar bonus per pesan sukses (`rate_referral`) ke pengundang pekerja (`u_sender.referred_by`), mencatat snapshot nilai `rate_snapshot` di `ledger_entries` jenis `referral`, menyertakan `source_user_id = u_sender.id`.
  2. Contoh kasus pemilik terverifikasi via tes konkurensi (`TestH6_FindingB2_OwnerExample_ABC`): Pekerja A mengajak B; B mengirim 100 pesan dengan rate Rp50 -> A mendapat Rp5.000. Pekerja B mengajak C; pesan C membayar bonus referral ke B, bukan ke A. Terbukti ketat satu tingkat (*strictly single-tier*).
  3. Anti-Fraud Guard: Pencegahan self-referral dan siklus referral. Penolakan bonus otomatis jika akun pengundang berstatus non-aktif atau dibekukan (`status NOT IN ('active', 'aktif')`). Penolakan bonus otomatis jika pengundang dan pekerja berbagi hash rekening tujuan penarikan yang sama (`u_ref.payout_dest_hash = u_sender.payout_dest_hash`), dicatat ke metrik engine `ReferralSkippedSameDestCount` tanpa mencatat entri buku besar (buku besar tetap bersih dari entri gagal). Idempotensi: constraint unik parsial `ON CONFLICT (campaign_number_id) WHERE kind = 'referral' DO NOTHING`.
  4. Seluruh tes di 16 paket repositori lulus 100%.

- 2026-10-15 (Tahap H5b — Keamanan Akses: Batas per Endpoint, CS, TOTP Admin, Audit):
  1. Rate Limiting per Endpoint: Semua rute POST dan GET JSON dilindungi token bucket `internal/ratelimit` tanpa blokir permanen.
  2. Keamanan CS & Tautan Publik: Validasi URL CS WhatsApp, CS Telegram, dan Channel Telegram wajib menggunakan skema `https://`; menolak keras skema `javascript:`, `data:`, `http:`.
  3. Logout Wajib POST: Rute `GET /logout` mengembalikan status `405 Method Not Allowed`. `POST /logout` mewajibkan token CSRF valid dan dibatasi lajunya via `RateLimitPublicMiddleware`.
  4. TOTP RFC 6238 untuk Admin: Implementasi kriptografi murni di `internal/auth/totp.go` (HMAC-SHA1, 30s, 6 digit, anti-replay `totp_last_step`). 10 kode pemulihan darurat sekali pakai (`XXXXX-XXXXX`) disimpan sebagai hash SHA-256. Step-up authentication 5 menit (`RequireStepUp`) untuk 10 aksi admin sensitif. Subcommand darurat CLI: `goblast reset-totp --email <email>`.
  5. Zero Error Leakage: Eliminasi total `err.Error()`, nama tabel DB, atau query SQL mentah ke HTTP response / URL query string.
  6. Audit Unggahan & Template: Verifikasi magic bytes XLSX (`PK\x03\x04`) dan gambar kampanye (JPEG/PNG 1 MB sniffing); penyimpanan UUID di luar web root. Audit template: 0 penggunaan `template.HTML` mentah.
  7. Seluruh tes pengawal dan 16 paket repositori lulus 100%.

- 2026-10-15 (Tahap H5a — Keamanan Akses: Login, Rate Limit, Sesi, Header, Turnstile):
  1. Penahanan brute force & DoS memori Argon2: `ArgonGate` (`ArgonConcurrencyLimit = 2`, `ArgonQueueLimit = 8`, `ArgonMaxWait = 2s`) + batas kata sandi maksimal 128 byte (`MaxPasswordBytes`). Uji benchmark 100 login serentak membatasi HeapAlloc puncak ke 323.99 MB dan menolak 92 permintaan dengan HTTP 503 `Retry-After: 2`.
  2. Mitigasi timing attack login: `ShadowHash` argon2id konstan untuk akun yang tidak ditemukan.
  3. Status pembekuan akun (`frozen`) hanya dicek SETELAH verifikasi kata sandi lolos.
  4. Sanitasi `entity_id` audit log dan throttling audit `login_failed` (20 log/menit per IP).
  5. Kuota sesi aktif maksimal 5 sesi simultan per user; pembersih berkala tiap jam; pencabutan sesi lain saat ganti kata sandi.
  6. Rate limiting multi-layer (Global, IP, Identifier, Pair, Register) dengan antarmuka admin `/admin/maintenance` pelepasan pembatasan aktif tanpa blokir permanen.
  7. Integrasi Cloudflare Turnstile, honeypot `website_hp`, dan Form Timing Token HMAC (2s–2h).
  8. Header keamanan lengkap: CSP dinamis `'nonce-{nonce}'`, penghapusan seluruh inline event handler, X-Content-Type-Options, X-Frame-Options, Referrer-Policy, HSTS, Cache-Control no-store.
  9. Seluruh 15 paket pengujian lulus 100%.

- 2026-10-15 (Tahap H4 — Pengelola Sesi whatsmeow Tahan Restart dan Gangguan):
  1. Rekonsiliasi boot B1: baris aktif diubah ke 'terputus' dengan stop_reason 'server restart', pembersihan perangkat yatim whatsmeow via `GetAllDevices` & `Delete`.
  2. Kuota sesi atomik `m.mu.Lock()` menggabungkan hitungan DB dan memori sehingga reservasi instan mencegah race condition (100 goroutine serentak tepat 10 lolos dan 90 ditolak antre).
  3. Pool pekerja event non-blocking (4 goroutine, antrean 64 per worker dengan partisi `accountID%4`).
  4. Matriks event whatsmeow: Connected, Disconnected (timer 5 menit DisconnectTimeout), LoggedOut, TemporaryBan, ClientOutdated (indikator merah di health check maintenance tanpa stop akun perorangan), StreamReplaced, PairSuccess verifikasi anti-duplikasi via HMAC-SHA256 `wa_jid_hash`.
  5. Penutupan anggun `StopAccountWait` (maks 10s) menyelesaikan kirim in-flight sebelum melepaskan FK.
  6. Seluruh 15 paket pengujian lulus 100%.

- 2026-10-15 (Tahap H3 — Mesin Kirim Tahan Ramai):
  1. Aturan kepemilikan nomor: hanya goroutine pengklaim yang memutasi baris `dikirim` via `WHERE id = $1 AND status = 'dikirim' AND claimed_account_id = $2`.
  2. Klasifikasi error whatsmeow 5 kelas (`ClassTerkirim`, `ClassPastiBelumTerkirim`, `ClassTidakPasti`, `ClassNomorTidakValid`, `ClassMasalahAkun`).
  3. Pemutus sirkuit: 5 kegagalan buruk beruntun menghentikan akun dengan stop_reason jelas.
  4. Anggaran kueri: tepat 2 round trip DB per pesan sukses via CTE atomik 1 RT (update nomor + insert ledger).
  5. `RecoverOnBoot` memulihkan nomor in-flight saat startup ke gagal hasil_tidak_pasti.
  6. Aksi admin antrekan ulang Keputusan H1 (`POST /admin/campaigns/{id}/requeue-uncertain`).
  7. Seluruh suite tes (15 paket) lulus 100%.

- 2026-10-15 (Tahap H2 — Skema Database Sebelum Produksi):
  1. EXPLAIN Benchmark 200.000 baris mock membuktikan 0 Sequential Scan (kueri pembersih stale 0.206ms, kueri pelepasan akun 0.991ms).
  2. Perbaikan Timezone B1 (Bug CURRENT_DATE vs Asia/Jakarta) dipusatkan ke paket `internal/timeutil` (WIB).
  3. Migrasi 0019–0023: indeks unik parsial `wa_jid_hash`, constraint & indeks unik referral ledger, indeks retensi web_sessions/audit_logs/notifications, indeks FK `number_batches.campaign_id`.
  4. Audit universal membuktikan 25 Foreign Key tanpa orphan row (0 orphan).
  5. Goose migration verification: Up 19–23 -> Down 18 -> Up 23 lulus 100%.

- 2026-10-15 (Tahap H1 — Fondasi Ketahanan Proses dan Koneksi DB):
  1. Paket `internal/safe` menangkap semua panic goroutine latar dan callback whatsmeow tanpa mematikan proses server.
  2. Pool `pgxpool` terkonfigurasi tahan macet (`DB_MAX_CONNS 25`, `DB_MIN_CONNS 5`, statement_timeout 30s, lock_timeout 5s).
  3. Pool whatsmeow terisolasi via driver pgx stdlib (`MaxOpenConns 10`, `MaxIdleConns 2`, `ConnMaxLifetime 30m`).
  4. Pengamanan HTTP: MaxBytesMiddleware (1 MB umum, 6 MB upload), MaxHeaderBytes 16 KB, neuteredFileSystem (mencegah directory listing).
  5. Graceful shutdown bertahap: SetDraining -> Engine.StopAll -> http.Server.Shutdown -> waManager.DisconnectAll -> db.Pool.Close.
  6. Pemisahan `/healthz` (liveness 200 OK) dan `/readyz` (readiness DB ping + non-draining).
  7. Seluruh suite tes lulus 100%.

---

### Tahap 6 – Tahap 12 dan Rangkaian S (S0 – S5)

- 2026-10-13 (Tahap S5 — Pengaturan Disederhanakan dan Akun Pekerja):
  - Formulir pengaturan admin `/admin/settings` disederhanakan hanya 4 isian bisnis (`rate_wage`, `rate_referral`, `rate_idr_to_usdt`, `max_accounts_per_worker`).
  - Parameter teknis backend (`speed_delays`, `daily_limits`, `max_attempts`, `max_consecutive_failures`, `claim_timeout_seconds`, `max_active_sessions`, `default_country_code`) dipindahkan ke konstanta paket di `internal/config/limits.go` dan env `MAX_ACTIVE_SESSIONS`.
  - Migrasi skema 0018 membersihkan 7 kunci lama dari tabel `settings`, menambahkan status `'frozen'`, kolom `freeze_reason`, `terms_accepted_at`, dan `terms_version` pada tabel `users`.
  - Pendaftaran pekerja dengan username wajib (3-20 karakter), email opsional (nullable), kata sandi min 10 karakter, persetujuan Syarat & Ketentuan.
  - Seluruh pengujian 14 paket lulus 100%.

- 2026-10-13 (Tahap S4 — Pengaman Penarikan):
  - Migrasi skema 0017 (`withdrawals.request_token` UNIQUE per user, `withdrawals.payout_reference`, `withdrawals.reject_reason`, `users.withdraw_locked_until`, `users.payout_dest_hash`, `users.must_change_password`).
  - Batas min penarikan IDR 10.000 dan USDT 5.000.000 mikro-USDT, max pending 1 penarikan, masa penahanan 24 jam (`withdraw_locked_until`) setelah ubah rekening/password.
  - Skema ciphertext berversi `v1:k1:<b64>` dan `v1:k0:<b64>` dengan fallback legacy dan rotasi kunci via `ENCRYPTION_KEY_PREV`.
  - Hash deterministik HMAC-SHA256 (`payout_dest_hash`) untuk pencarian admin dan deteksi multi-akun tanpa membuka enkripsi.

- 2026-10-13 (Tahap S3 — Pusat Kendali & Diagnostik Sistem):
  - Tiga kunci granular di tabel `settings` (`lock_withdrawals`, `lock_linking`, `lock_engine`) dan kolom pesan pekerja (`lock_message`) dengan audit log.
  - Engine memeriksa kunci dan mode pemeliharaan via cache memori 2 detik, berhenti mengklaim nomor baru dengan stop_reason "sistem dalam perbaikan" tanpa memutus sesi whatsmeow.
  - Halaman `/admin/maintenance` dengan saklar darurat dan 9 pemeriksaan kesehatan sistem (latensi DB, versi migrasi, engine kirim, sesi WA terhubung vs terdaftar, nomor menggantung, penarikan >24 jam, usia kurs USDT, verifikasi enkripsi AES-256-GCM, rekonsiliasi buku besar atomik).

- 2026-10-13 (Tahap S2 — Satu Definisi Nomor Tersedia):
  - Pemusatan kriteria claimable di `internal/campaign/claimable.go` (`ClaimableWhere`: `cn.status = 'tersedia' AND c.status = 'berjalan' AND c.archived_at IS NULL`).
  - Dashboard admin menampilkan metrik "tertahan" (`TertahanWhere`).
  - Endpoint `GET /worker/api/stats` dengan cache in-memory 2 detik untuk metrik global; polling live-update 5 detik pada antarmuka pekerja.
  - Pengawal tes statis (`guard_test.go`) menolak query `status = 'tersedia'` ad-hoc di luar `claimable.go` dan `engine.go`.

- 2026-10-13 (Tahap S1 — Mata Uang Tunggal IDR & Konversi USDT):
  - Semua saldo pekerja dalam Rupiah (IDR); kolom `users.currency` dihapus; USDT hanya didapat lewat konversi eksplisit "Tukar ke USDT" dengan `request_token` idempoten dan batas usia kurs 24 jam (`KursMaxAgeHours`).
  - Trigger pencegah saldo negatif diperluas untuk penarikan dan konversi; matematika uang integer murni tanpa float (`internal/money`).

- 2026-10-12 (Penyempurnaan Panel Admin & Worker):
  - Dual layout responsif mobile & desktop di seluruh tabel panel admin dan pekerja.
  - Balasan tiket CS admin (`reply_message`) terintegrasi ke notifikasi dan modal bantuan pekerja.
  - Sinkronisasi otomatis broadcast global ke notifikasi lonceng pekerja.
  - Konfigurasi tautan CS resmi via environment variables (`CS_WHATSAPP_URL`, `CS_TELEGRAM_URL`, `CHANNEL_TELEGRAM_URL`).

- 2026-10-12 (Koreksi Tahap 10):
  - Migrasi 0010_remove_seeded_business_defaults.sql memindahkan default bisnis ke dev.sql dengan proteksi penolakan di produksi.
  - Format rate upah disatukan menjadi JSON `rate_wage`.
  - Pencatatan pasca-kirim dibungkus context.WithoutCancel dengan timeout 10s dan 3x retry.
  - Pengiriman sender.Send dibungkus context.WithTimeout 60s. Error batas waktu berstatus 'gagal' (last_error "hasil_tidak_pasti") tanpa ulang.
  - Thumbnail di-cache dalam memori dengan kunci (path gambar, waktu ubah file).
  - Batas harian dihitung dari awal hari zona Asia/Jakarta (00:00:00 WIB).

- 2026-10-11 (Tahap 11 — Panel Pekerja):
  - Halaman `/worker`: ringkasan penghasilan per mata uang dari `ledger_entries`, daftar akun WhatsApp pekerja langsung di beranda, dan sisa nomor tersedia agregat.
  - Akun WhatsApp (`/worker/accounts`): modal persetujuan risiko banned & privasi sesi wajib dicentang sebelum menyambung akun pertama. Dropdown 4 kecepatan.
  - Riwayat Pesan (`/worker/history`): riwayat pesan terkirim tanpa nomor customer sama sekali demi privasi data.
  - Referral (`/worker/referral`): kode & tautan referral.
  - Arsitektur Offcanvas Drawer Ganda yang simetris dan responsif.

- 2026-10-11 (Tahap 10 — Mesin Kirim):
  - Uji klaim atomik konkuren 50 akun mengklaim 1000 nomor berhasil (0 duplikat, semua terklaim tepat 1 kali).
  - Uji buku besar idempoten lulus (ON CONFLICT DO NOTHING via unique index).
  - Uji pelepasan nomor saat putus sesi dan pembersih stale numbers (make_interval) lulus.

- 2026-10-11 (Tahap 9 — Pengelola Sesi WA):
  - `internal/wa.Manager` memegang peta sesi aktif, membatasi akun per pekerja dan total sesi aktif dari settings, menangani QR via `go-qrcode` dan pairing phone code, mengabaikan event pesan masuk untuk privasi.
  - Handlers dan template HTML (`worker_accounts.html`, `worker_account_new.html`) selesai dengan polling JSON 3 detik. Migrasi 0006 menambahkan kolom auto-stop dan device ID.

---

### Tahap 0 – Tahap 5

- 2026-10-11: .env dari pemilik berhasil dibaca. Perintah `migrate status` merespons koneksi sukses tapi tidak ada file migrasi. Endpoint `/healthz` berjalan dan membalas sukses ({"status":"ok","db":"ok"}).
- 2026-10-11: Pemilik proyek memperbarui Go menjadi 1.27.2. Tahap 4 dilanjutkan: direktori dibuat, .env dan config diatur, go get sukses, theme.css diimplementasi dengan variabel warna sesuai bagian 17, MaskPhone dengan uji kasus ditulis, dan tes translasi berlalu dengan fallback ke id.
- 2026-10-11: Proses `go get` untuk `github.com/jackc/pgx/v5` dan `github.com/pressly/goose/v3` terblokir karena pustaka meminta Go versi >= 1.25.0 dan >= 1.26.0, sedangkan versi terpasang adalah 1.22.2. Eksekusi dihentikan sesuai aturan di DOC_AI_SLOP bagian 14.
- 2026-10-10: Berdasarkan dokumentasi whatsmeow, event receipt dibedakan menjadi TypeSender (centang 1) dan TypeDelivered (centang 2). Memori per sesi diprediksi sangat kecil (5-10MB idle), tapi bisa naik signifikan untuk 100 sesi jika ada antrean pesan. Perintah client.Logout() akan menghapus sesi lokal di database (sqlstore).
- 2026-10-10: Repo git diinisialisasi dengan struktur .gitignore yang melarang direktori data/unggahan. DOC_SECURITY_DEPLOY siap dengan panduan rate-limiting, proteksi privasi customer, dan arsitektur VPS.
- 2026-10-10: Cek lingkungan berhasil. Go (go1.22.2 windows/amd64), PostgreSQL (17.11), dan Git (2.53.0.windows.1) terdeteksi. pg_isready menerima koneksi di port 5432.
