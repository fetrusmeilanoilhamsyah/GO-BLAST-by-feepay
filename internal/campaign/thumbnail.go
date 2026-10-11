package campaign

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"log/slog"
	"os"
	"sync"

	"golang.org/x/image/draw"
)

// ThumbnailSpec sementara (TODO: konfirmasi setelah uji send-card-thumb, lihat spike/HASIL.md bagian 4)
const (
	DefaultThumbWidth   = 300
	DefaultThumbQuality = 70
)

type thumbCacheKey struct {
	path    string
	modTime int64
}

var (
	thumbCacheMu sync.RWMutex
	thumbCache   = make(map[thumbCacheKey][]byte)
)

// ResetThumbnailCache mengosongkan cache memori thumbnail (berguna untuk tes).
func ResetThumbnailCache() {
	thumbCacheMu.Lock()
	defer thumbCacheMu.Unlock()
	thumbCache = make(map[thumbCacheKey][]byte)
}

// GetOrCreateThumbnail mengambil thumbnail dari cache memori dengan kunci (path gambar, waktu ubah file).
// Jika belum ada di cache, thumbnail digenerate sekali dan disimpan.
// Error pembuatannya dicatat di log dan tidak diabaikan.
func GetOrCreateThumbnail(imagePath string, targetW, quality int) ([]byte, error) {
	if imagePath == "" {
		return nil, nil
	}

	fi, err := os.Stat(imagePath)
	if err != nil {
		slog.Error("campaign: stat gambar thumbnail gagal", "image_path", imagePath, "err", err)
		return nil, fmt.Errorf("stat gambar thumbnail: %w", err)
	}

	key := thumbCacheKey{
		path:    imagePath,
		modTime: fi.ModTime().UnixNano(),
	}

	thumbCacheMu.RLock()
	cached, found := thumbCache[key]
	thumbCacheMu.RUnlock()
	if found {
		return cached, nil
	}

	data, err := GenerateThumbnail(imagePath, targetW, quality)
	if err != nil {
		slog.Error("campaign: pembuatan thumbnail gagal", "image_path", imagePath, "err", err)
		return nil, err
	}

	thumbCacheMu.Lock()
	thumbCache[key] = data
	thumbCacheMu.Unlock()

	return data, nil
}

// GenerateThumbnail membaca file gambar dari imagePath, mengubah ukurannya menjadi lebar targetW
// (tinggi proporsional), dan mengembalikan bytes JPEG dengan kualitas quality (1-100).
func GenerateThumbnail(imagePath string, targetW int, quality int) ([]byte, error) {
	if imagePath == "" {
		return nil, nil
	}
	f, err := os.Open(imagePath)
	if err != nil {
		return nil, fmt.Errorf("buka gambar: %w", err)
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode gambar: %w", err)
	}

	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil, fmt.Errorf("dimensi gambar tidak valid: %dx%d", srcW, srcH)
	}

	targetH := targetW * srcH / srcW
	if targetH < 1 {
		targetH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}

	return buf.Bytes(), nil
}
