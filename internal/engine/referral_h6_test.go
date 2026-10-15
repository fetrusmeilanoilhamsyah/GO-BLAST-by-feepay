package engine_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"goblast/internal/db"
	"goblast/internal/engine"
	"goblast/internal/testutil"
	"goblast/internal/wa/watest"
)

// helper untuk membuat worker di database tes
func createTestWorkerWithReferrer(t *testing.T, pool *pgxpool.Pool, username string, referredBy *int64, payoutDestHash *string, status string) int64 {
	t.Helper()
	var id int64
	email := fmt.Sprintf("%s_%d@test.local", username, time.Now().UnixNano())
	refCode := fmt.Sprintf("REF_%s_%d", username, time.Now().UnixNano())
	if status == "" {
		status = "active"
	}

	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, username, password_hash, role, display_name, status, referral_code, referred_by, payout_dest_hash)
		 VALUES ($1, $2, 'hash', 'worker', $2, $3, $4, $5, $6)
		 RETURNING id`,
		email, username, status, refCode, referredBy, payoutDestHash,
	).Scan(&id)
	if err != nil {
		t.Fatalf("createTestWorkerWithReferrer(%s) gagal: %v", username, err)
	}
	return id
}

// helper untuk setup campaign dan batch
func createTestCampaignAndBatch(t *testing.T, pool *pgxpool.Pool, creatorID int64) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	var campaignID int64
	err := pool.QueryRow(ctx,
		`INSERT INTO campaigns (name, status, button_mode, body, elements, created_by)
		 VALUES ($1, 'berjalan', 'kartu_link', 'Halo', '{}', $2)
		 RETURNING id`,
		fmt.Sprintf("Campaign H6 %d", time.Now().UnixNano()), creatorID,
	).Scan(&campaignID)
	if err != nil {
		t.Fatalf("setup campaign gagal: %v", err)
	}

	var batchID int64
	err = pool.QueryRow(ctx,
		`INSERT INTO number_batches (campaign_id, filename, uploaded_by, total, accepted)
		 VALUES ($1, 'test.csv', $2, 100, 100) RETURNING id`,
		campaignID, creatorID,
	).Scan(&batchID)
	if err != nil {
		t.Fatalf("setup batch gagal: %v", err)
	}
	return campaignID, batchID
}

// helper membuat akun WhatsApp
func createTestWAAccount(t *testing.T, pool *pgxpool.Pool, workerID int64) int64 {
	t.Helper()
	var accID int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO wa_accounts (worker_id, label, wa_jid_hash, status, speed)
		 VALUES ($1, 'Akun WA', $2, 'terhubung', 'fast') RETURNING id`,
		workerID, fmt.Sprintf("jid_hash_%d", time.Now().UnixNano()),
	).Scan(&accID)
	if err != nil {
		t.Fatalf("setup wa_account gagal: %v", err)
	}
	return accID
}

// helper membuat nomor campaign siap kirim (status = 'dikirim')
func createTestClaimedNumber(t *testing.T, pool *pgxpool.Pool, campaignID, batchID, accID int64, phoneSuffix string) int64 {
	t.Helper()
	var numID int64
	phone := fmt.Sprintf("628%09d", time.Now().UnixNano()%1000000000)
	if phoneSuffix != "" {
		phone = phoneSuffix
	}
	err := pool.QueryRow(context.Background(),
		`INSERT INTO campaign_numbers (campaign_id, batch_id, phone_e164, country_code, status, claimed_account_id, claimed_at)
		 VALUES ($1, $2, $3, '62', 'dikirim', $4, now()) RETURNING id`,
		campaignID, batchID, phone, accID,
	).Scan(&numID)
	if err != nil {
		t.Fatalf("setup claimed campaign_number gagal: %v", err)
	}
	return numID
}

// 1. Bonus benar: A diajak B, B mengirim 10 pesan dengan rate_referral Rp50.
// A menerima tepat 10 baris referral dengan jumlah Rp50 dan rate_snapshot Rp50.
// Upah B tidak berubah (Rp 100 per pesan).
func TestH6_FindingB2_ReferralBonus_CorrectAmountAndSnapshot(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	workerA := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("wa_%d", time.Now().UnixNano()%100000), nil, nil, "active")
	workerB := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("wb_%d", time.Now().UnixNano()%100000), &workerA, nil, "active")
	campID, batchID := createTestCampaignAndBatch(t, pool, workerA)
	accID := createTestWAAccount(t, pool, workerB)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerA, workerB}, []int64{campID})
	})

	database := &db.DB{Pool: pool}
	settings := newTestSettings()
	settings.SetJSON("rate_wage", "100")
	settings.SetInt("rate_referral", 50)
	eng := engine.New(database, &watest.FakeSender{}, nil, settings)

	// B mengirim 10 pesan
	const numMessages = 10
	var numberIDs []int64
	for i := 0; i < numMessages; i++ {
		numID := createTestClaimedNumber(t, pool, campID, batchID, accID, fmt.Sprintf("628111%06d", i))
		numberIDs = append(numberIDs, numID)

		err := eng.RecordSuccessAtomic(ctx, numID, accID, workerB, 100, 50)
		if err != nil {
			t.Fatalf("pesan %d: RecordSuccessAtomic gagal: %v", i, err)
		}
	}

	// Cek upah worker B: harus tepat 10 baris @ 100 IDR
	var bCount int
	var bTotal int64
	err := pool.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM ledger_entries WHERE user_id = $1 AND kind = 'upah'`, workerB).Scan(&bCount, &bTotal)
	if err != nil {
		t.Fatalf("query upah B gagal: %v", err)
	}
	if bCount != 10 || bTotal != 1000 {
		t.Fatalf("upah B salah: count = %d (want 10), total = %d (want 1000)", bCount, bTotal)
	}

	// Cek bonus referral worker A: harus tepat 10 baris @ 50 IDR
	var aCount int
	var aTotal int64
	err = pool.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral'`, workerA).Scan(&aCount, &aTotal)
	if err != nil {
		t.Fatalf("query referral A gagal: %v", err)
	}
	if aCount != 10 || aTotal != 500 {
		t.Fatalf("referral A salah: count = %d (want 10), total = %d (want 500)", aCount, aTotal)
	}

	// Cek detail baris referral A: source_user_id = B, rate_snapshot = 50, currency = IDR
	rows, err := pool.Query(ctx, `SELECT rate_snapshot, source_user_id, currency, note FROM ledger_entries WHERE user_id = $1 AND kind = 'referral'`, workerA)
	if err != nil {
		t.Fatalf("query detail referral gagal: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rateSnap int64
		var sourceUser int64
		var curr, note string
		if err := rows.Scan(&rateSnap, &sourceUser, &curr, &note); err != nil {
			t.Fatalf("scan detail referral gagal: %v", err)
		}
		if rateSnap != 50 {
			t.Errorf("rate_snapshot = %d, want 50", rateSnap)
		}
		if sourceUser != workerB {
			t.Errorf("source_user_id = %d, want %d", sourceUser, workerB)
		}
		if curr != "IDR" {
			t.Errorf("currency = %q, want IDR", curr)
		}
	}
}

// 2. Contoh dari pemilik: A mengajak B; B mengajak C.
// B mengirim 100 pesan dan C mengirim 100 pesan dengan rate_referral Rp50.
// A mendapat Rp5.000 (dari B) dan tidak ada dari C.
// B mendapat Rp5.000 (dari C).
// C mendapat Rp0 dari referral.
func TestH6_FindingB2_OwnerExample_ABC(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	workerA := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("oa_%d", time.Now().UnixNano()%100000), nil, nil, "active")
	workerB := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("ob_%d", time.Now().UnixNano()%100000), &workerA, nil, "active")
	workerC := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("oc_%d", time.Now().UnixNano()%100000), &workerB, nil, "active")
	campID, batchID := createTestCampaignAndBatch(t, pool, workerA)

	accB := createTestWAAccount(t, pool, workerB)
	accC := createTestWAAccount(t, pool, workerC)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerA, workerB, workerC}, []int64{campID})
	})

	database := &db.DB{Pool: pool}
	settings := newTestSettings()
	eng := engine.New(database, &watest.FakeSender{}, nil, settings)

	// B mengirim 100 pesan
	for i := 0; i < 100; i++ {
		numID := createTestClaimedNumber(t, pool, campID, batchID, accB, fmt.Sprintf("628221%06d", i))
		err := eng.RecordSuccessAtomic(ctx, numID, accB, workerB, 50, 50)
		if err != nil {
			t.Fatalf("B pesan %d gagal: %v", i, err)
		}
	}

	// C mengirim 100 pesan
	for i := 0; i < 100; i++ {
		numID := createTestClaimedNumber(t, pool, campID, batchID, accC, fmt.Sprintf("628331%06d", i))
		err := eng.RecordSuccessAtomic(ctx, numID, accC, workerC, 50, 50)
		if err != nil {
			t.Fatalf("C pesan %d gagal: %v", i, err)
		}
	}

	// Verifikasi penghasilan referral A: tepat Rp 5.000, seluruhnya dari B, 0 dari C
	var aRefTotal int64
	var aFromB, aFromC int
	err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral'`, workerA).Scan(&aRefTotal)
	if err != nil {
		t.Fatalf("query referral A gagal: %v", err)
	}
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral' AND source_user_id = $2`, workerA, workerB).Scan(&aFromB)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral' AND source_user_id = $2`, workerA, workerC).Scan(&aFromC)

	if aRefTotal != 5000 {
		t.Errorf("A referral total = %d, want 5000", aRefTotal)
	}
	if aFromB != 100 {
		t.Errorf("A referral entries from B = %d, want 100", aFromB)
	}
	if aFromC != 0 {
		t.Errorf("A referral entries from C = %d, want 0 (tidak boleh ada bonus berantai!)", aFromC)
	}

	// Verifikasi penghasilan referral B: tepat Rp 5.000, seluruhnya dari C
	var bRefTotal int64
	var bFromC int
	_ = pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral'`, workerB).Scan(&bRefTotal)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral' AND source_user_id = $2`, workerB, workerC).Scan(&bFromC)

	if bRefTotal != 5000 {
		t.Errorf("B referral total = %d, want 5000", bRefTotal)
	}
	if bFromC != 100 {
		t.Errorf("B referral entries from C = %d, want 100", bFromC)
	}

	// Verifikasi C tidak mendapat bonus referral
	var cRefTotal int64
	_ = pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral'`, workerC).Scan(&cRefTotal)
	if cRefTotal != 0 {
		t.Errorf("C referral total = %d, want 0", cRefTotal)
	}
}

// 3. Tidak ganda: 50 pencatatan konkuren untuk nomor yang sama menghasilkan tepat satu baris referral
func TestH6_FindingB2_ReferralIdempotency_Concurrent50(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	workerA := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("ca_%d", time.Now().UnixNano()%100000), nil, nil, "active")
	workerB := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("cb_%d", time.Now().UnixNano()%100000), &workerA, nil, "active")
	campID, batchID := createTestCampaignAndBatch(t, pool, workerA)
	accID := createTestWAAccount(t, pool, workerB)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerA, workerB}, []int64{campID})
	})

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, newTestSettings())

	numID := createTestClaimedNumber(t, pool, campID, batchID, accID, "62899990050")

	// 50 goroutine serentak memanggil RecordSuccessAtomic untuk nomor yang sama
	var wg sync.WaitGroup
	const concurrency = 50
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			_ = eng.RecordSuccessAtomic(ctx, numID, accID, workerB, 50, 50)
		}()
	}
	wg.Wait()

	// Pastikan baris upah tepat 1
	var upahCount int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE campaign_number_id = $1 AND kind = 'upah'`, numID).Scan(&upahCount)
	if err != nil {
		t.Fatalf("query upah count gagal: %v", err)
	}
	if upahCount != 1 {
		t.Errorf("upahCount = %d, want 1", upahCount)
	}

	// Pastikan baris referral tepat 1
	var refCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE campaign_number_id = $1 AND kind = 'referral'`, numID).Scan(&refCount)
	if err != nil {
		t.Fatalf("query ref count gagal: %v", err)
	}
	if refCount != 1 {
		t.Errorf("refCount = %d, want 1", refCount)
	}
}

// 4. Referrer dibekukan atau dinonaktifkan: tidak ada baris.
// Diaktifkan lagi: pesan berikutnya dibayar, yang lampau tidak.
func TestH6_FindingB2_ReferrerInactiveOrFrozen_NoBonus(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	// A berstatus 'frozen'
	workerA := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("fa_%d", time.Now().UnixNano()%100000), nil, nil, "frozen")
	workerB := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("fb_%d", time.Now().UnixNano()%100000), &workerA, nil, "active")
	campID, batchID := createTestCampaignAndBatch(t, pool, workerA)
	accID := createTestWAAccount(t, pool, workerB)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerA, workerB}, []int64{campID})
	})

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, newTestSettings())

	// B kirim pesan saat A frozen
	numID1 := createTestClaimedNumber(t, pool, campID, batchID, accID, "62899990051")
	err := eng.RecordSuccessAtomic(ctx, numID1, accID, workerB, 50, 50)
	if err != nil {
		t.Fatalf("pesan 1 gagal: %v", err)
	}

	// A tidak boleh menerima bonus referral saat frozen
	var aBonusCount int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral'`, workerA).Scan(&aBonusCount)
	if aBonusCount != 0 {
		t.Errorf("bonus referral harus 0 saat referrer frozen, dapat %d", aBonusCount)
	}

	// Aktifkan kembali A
	_, err = pool.Exec(ctx, `UPDATE users SET status = 'active' WHERE id = $1`, workerA)
	if err != nil {
		t.Fatalf("unfreeze A gagal: %v", err)
	}

	// B kirim pesan berikutnya
	numID2 := createTestClaimedNumber(t, pool, campID, batchID, accID, "62899990052")
	err = eng.RecordSuccessAtomic(ctx, numID2, accID, workerB, 50, 50)
	if err != nil {
		t.Fatalf("pesan 2 gagal: %v", err)
	}

	// Sekarang A harus menerima tepat 1 baris bonus referral (hanya untuk pesan ke-2, pesan ke-1 tidak dibayar mundur)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral'`, workerA).Scan(&aBonusCount)
	if aBonusCount != 1 {
		t.Errorf("bonus referral setelah unfreeze harus tepat 1, dapat %d", aBonusCount)
	}
}

// 5. payout_dest_hash sama: tidak ada baris bonus referral (pencegahan kecurangan D).
func TestH6_FindingB2_SamePayoutDestHash_NoBonus(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	sameHash := "sha256_mock_dest_hash_12345"
	workerA := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("ha_%d", time.Now().UnixNano()%100000), nil, &sameHash, "active")
	workerB := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("hb_%d", time.Now().UnixNano()%100000), &workerA, &sameHash, "active")
	campID, batchID := createTestCampaignAndBatch(t, pool, workerA)
	accID := createTestWAAccount(t, pool, workerB)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerA, workerB}, []int64{campID})
	})

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, newTestSettings())

	numID := createTestClaimedNumber(t, pool, campID, batchID, accID, "62899990053")
	err := eng.RecordSuccessAtomic(ctx, numID, accID, workerB, 50, 50)
	if err != nil {
		t.Fatalf("kirim gagal: %v", err)
	}

	// Upah B tetap masuk
	var bUpah int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'upah'`, workerB).Scan(&bUpah)
	if bUpah != 1 {
		t.Errorf("upah B harus masuk 1, dapat %d", bUpah)
	}

	// Bonus referral A DITOLAK (0 baris) karena rekening/wallet sama
	var aRef int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral'`, workerA).Scan(&aRef)
	if aRef != 0 {
		t.Errorf("bonus referral A harus 0 karena payout_dest_hash sama, dapat %d", aRef)
	}
}

// 6. rate_referral 0: tidak ada baris dan tidak ada galat
func TestH6_FindingB2_RateReferralZero_NoBonusNoErr(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	workerA := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("za_%d", time.Now().UnixNano()%100000), nil, nil, "active")
	workerB := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("zb_%d", time.Now().UnixNano()%100000), &workerA, nil, "active")
	campID, batchID := createTestCampaignAndBatch(t, pool, workerA)
	accID := createTestWAAccount(t, pool, workerB)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerA, workerB}, []int64{campID})
	})

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, newTestSettings())

	numID := createTestClaimedNumber(t, pool, campID, batchID, accID, "62899990054")
	err := eng.RecordSuccessAtomic(ctx, numID, accID, workerB, 50, 0)
	if err != nil {
		t.Fatalf("RecordSuccessAtomic error when rate_referral is 0: %v", err)
	}

	var aRef int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'referral'`, workerA).Scan(&aRef)
	if aRef != 0 {
		t.Errorf("bonus referral harus 0 jika rate_referral 0, dapat %d", aRef)
	}
}

// 7. rate_referral berubah: baris lama tidak berubah (rate_snapshot)
func TestH6_FindingB2_RateReferralChange_SnapshotImmutable(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	workerA := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("ra_%d", time.Now().UnixNano()%100000), nil, nil, "active")
	workerB := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("rb_%d", time.Now().UnixNano()%100000), &workerA, nil, "active")
	campID, batchID := createTestCampaignAndBatch(t, pool, workerA)
	accID := createTestWAAccount(t, pool, workerB)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerA, workerB}, []int64{campID})
	})

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, newTestSettings())

	// Pesan 1 saat rate_referral = 50
	numID1 := createTestClaimedNumber(t, pool, campID, batchID, accID, "62899990055")
	err := eng.RecordSuccessAtomic(ctx, numID1, accID, workerB, 50, 50)
	if err != nil {
		t.Fatalf("pesan 1 gagal: %v", err)
	}

	// Pesan 2 saat rate_referral dinaikkan ke 75
	numID2 := createTestClaimedNumber(t, pool, campID, batchID, accID, "62899990056")
	err = eng.RecordSuccessAtomic(ctx, numID2, accID, workerB, 50, 75)
	if err != nil {
		t.Fatalf("pesan 2 gagal: %v", err)
	}

	var snap1, amt1, snap2, amt2 int64
	_ = pool.QueryRow(ctx, `SELECT rate_snapshot, amount FROM ledger_entries WHERE campaign_number_id = $1 AND kind = 'referral'`, numID1).Scan(&snap1, &amt1)
	_ = pool.QueryRow(ctx, `SELECT rate_snapshot, amount FROM ledger_entries WHERE campaign_number_id = $1 AND kind = 'referral'`, numID2).Scan(&snap2, &amt2)

	if snap1 != 50 || amt1 != 50 {
		t.Errorf("pesan 1: snap = %d, amt = %d; want 50", snap1, amt1)
	}
	if snap2 != 75 || amt2 != 75 {
		t.Errorf("pesan 2: snap = %d, amt = %d; want 75", snap2, amt2)
	}
}

// 8. Saldo referrer bisa ditarik lewat jalur penarikan biasa, trigger saldo tidak terganggu
func TestH6_FindingB2_ReferrerBalance_Withdrawal(t *testing.T) {
	pool := getTestDB(t)
	ctx := context.Background()

	workerA := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("wa_wd_%d", time.Now().UnixNano()%100000), nil, nil, "active")
	workerB := createTestWorkerWithReferrer(t, pool, fmt.Sprintf("wb_wd_%d", time.Now().UnixNano()%100000), &workerA, nil, "active")
	campID, batchID := createTestCampaignAndBatch(t, pool, workerA)
	accID := createTestWAAccount(t, pool, workerB)

	t.Cleanup(func() {
		testutil.CleanupTestData(pool, []int64{workerA, workerB}, []int64{campID})
	})

	database := &db.DB{Pool: pool}
	eng := engine.New(database, &watest.FakeSender{}, nil, newTestSettings())

	// B kirim 20 pesan @ rate_referral 1000 = Rp 20.000 untuk A
	for i := 0; i < 20; i++ {
		numID := createTestClaimedNumber(t, pool, campID, batchID, accID, fmt.Sprintf("628441%06d", i))
		err := eng.RecordSuccessAtomic(ctx, numID, accID, workerB, 50, 1000)
		if err != nil {
			t.Fatalf("pesan %d gagal: %v", i, err)
		}
	}

	// Cek saldo A via user_balance
	var balA int64
	err := pool.QueryRow(ctx, `SELECT user_balance($1, 'IDR')`, workerA).Scan(&balA)
	if err != nil {
		t.Fatalf("query balance gagal: %v", err)
	}
	if balA != 20000 {
		t.Fatalf("saldo A = %d, want 20000", balA)
	}

	// Tarik Rp 15.000 dari saldo A via penarikan
	_, err = pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'penarikan', 'IDR', -15000, 'penarikan saldo bonus referral')
	`, workerA)
	if err != nil {
		t.Fatalf("debit penarikan gagal: %v", err)
	}

	// Cek sisa saldo A
	_ = pool.QueryRow(ctx, `SELECT user_balance($1, 'IDR')`, workerA).Scan(&balA)
	if balA != 5000 {
		t.Errorf("sisa saldo A = %d, want 5000", balA)
	}

	// Coba tarik melebihi saldo (Rp 10.000 padahal sisa 5.000) -> harus ditolak trigger trg_check_withdrawal_balance
	_, err = pool.Exec(ctx, `
		INSERT INTO ledger_entries (user_id, kind, currency, amount, note)
		VALUES ($1, 'penarikan', 'IDR', -10000, 'penarikan melebihi saldo')
	`, workerA)
	if err == nil {
		t.Errorf("expected trigger rejection on overdraft withdrawal")
	}
}
