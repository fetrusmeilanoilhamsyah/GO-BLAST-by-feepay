package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"goblast/internal/campaign"
	"goblast/internal/db"
)

// SettingsSnapshot menampung nilai konfigurasi bisnis dan sistem yang sering dibaca.
type SettingsSnapshot struct {
	RateWage        int64
	RateReferral    int64
	LockEngine      bool
	MaintenanceMode bool
	UpdatedAt       time.Time
}

// SettingsCache mengelola pembaruan snapshot pengaturan terpusat (tiap 2-5 detik).
type SettingsCache struct {
	mu       sync.RWMutex
	snapshot SettingsSnapshot
}

func (c *SettingsCache) Get() SettingsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

func (c *SettingsCache) Set(s SettingsSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot = s
}

// CachedCampaign menampung metadata pesan campaign dengan TTL 15 detik.
type CachedCampaign struct {
	Body       string
	ButtonMode string
	ImagePath  string
	Elements   campaign.Elements
	CachedAt   time.Time
}

// CampaignCache mengelola cache metadata pesan campaign agar tidak ada query DB per pesan.
type CampaignCache struct {
	mu    sync.RWMutex
	items map[int64]CachedCampaign
}

func newCampaignCache() *CampaignCache {
	return &CampaignCache{
		items: make(map[int64]CachedCampaign),
	}
}

func (c *CampaignCache) Get(campaignID int64) (CachedCampaign, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, ok := c.items[campaignID]
	if !ok || time.Since(item.CachedAt) > 15*time.Second {
		return CachedCampaign{}, false
	}
	return item, true
}

func (c *CampaignCache) Set(campaignID int64, item CachedCampaign) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item.CachedAt = time.Now()
	c.items[campaignID] = item
}

func (c *CampaignCache) Invalidate(campaignID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, campaignID)
}

func (c *CampaignCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[int64]CachedCampaign)
}

// CachedAccount menampung status dan kecepatan akun dengan TTL 10 detik.
type CachedAccount struct {
	Speed    string
	Status   string
	CachedAt time.Time
}

// AccountCache mengelola cache status akun di memori.
type AccountCache struct {
	mu    sync.RWMutex
	items map[int64]CachedAccount
}

func newAccountCache() *AccountCache {
	return &AccountCache{
		items: make(map[int64]CachedAccount),
	}
}

func (c *AccountCache) Get(accountID int64) (CachedAccount, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, ok := c.items[accountID]
	if !ok || time.Since(item.CachedAt) > 10*time.Second {
		return CachedAccount{}, false
	}
	return item, true
}

func (c *AccountCache) Set(accountID int64, speed, status string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[accountID] = CachedAccount{
		Speed:    speed,
		Status:   status,
		CachedAt: time.Now(),
	}
}

func (c *AccountCache) Invalidate(accountID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, accountID)
}

// fetchCampaignFromDB memuat data campaign dari PostgreSQL jika belum ada di cache.
func fetchCampaignFromDB(ctx context.Context, database *db.DB, campaignID int64) (CachedCampaign, error) {
	var body, mode, imagePath sql.NullString
	var elementsRaw []byte

	err := database.Pool.QueryRow(ctx,
		`SELECT body, button_mode, COALESCE(image_path, ''), elements FROM campaigns WHERE id = $1`,
		campaignID,
	).Scan(&body, &mode, &imagePath, &elementsRaw)
	if err != nil {
		return CachedCampaign{}, err
	}

	var elem campaign.Elements
	if len(elementsRaw) > 0 {
		_ = json.Unmarshal(elementsRaw, &elem)
	}

	return CachedCampaign{
		Body:       body.String,
		ButtonMode: mode.String,
		ImagePath:  imagePath.String,
		Elements:   elem,
		CachedAt:   time.Now(),
	}, nil
}
