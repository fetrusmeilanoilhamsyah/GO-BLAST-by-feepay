package campaign

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	MaxImageUploadBytes = 5 * 1024 * 1024 // 5 MB
	MaxImageDimension   = 4096            // 4096 px sisi terpanjang
)

// ProcessAndSaveCampaignImage memvalidasi magic bytes, membatasi dimensi,
// mendekode lalu mengenkode ulang gambar untuk membuang metadata EXIF (termasuk GPS),
// dan menyimpannya dengan nama acak ke folder uploads/.
func ProcessAndSaveCampaignImage(data []byte, originalFilename string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("file gambar kosong")
	}
	if len(data) > MaxImageUploadBytes {
		return "", errors.New("ukuran file melebihi batas (maksimal 5 MB)")
	}

	contentType := http.DetectContentType(data)
	var ext string
	if contentType == "image/jpeg" {
		ext = ".jpg"
	} else if contentType == "image/png" {
		ext = ".png"
	} else {
		return "", errors.New("Hanya menerima gambar JPEG atau PNG")
	}

	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("gagal mendekode gambar: %w", err)
	}

	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w <= 0 || h <= 0 {
		return "", errors.New("dimensi gambar tidak valid")
	}
	if w > MaxImageDimension || h > MaxImageDimension {
		return "", fmt.Errorf("dimensi gambar (%dx%d) melebihi batas maksimal %d piksel", w, h, MaxImageDimension)
	}

	// Enkode ulang untuk membuang seluruh metadata EXIF / profil warna bawaan
	var cleanBuf bytes.Buffer
	if format == "png" || contentType == "image/png" {
		if err := png.Encode(&cleanBuf, img); err != nil {
			return "", fmt.Errorf("gagal mengenkode ulang PNG: %w", err)
		}
	} else {
		if err := jpeg.Encode(&cleanBuf, img, &jpeg.Options{Quality: 85}); err != nil {
			return "", fmt.Errorf("gagal mengenkode ulang JPEG: %w", err)
		}
	}

	// Nama acak 16 byte hex
	rnd := make([]byte, 16)
	if _, err := rand.Read(rnd); err != nil {
		return "", fmt.Errorf("gagal membuat nama file acak: %w", err)
	}
	filename := hex.EncodeToString(rnd) + ext

	if err := os.MkdirAll("uploads", 0755); err != nil {
		return "", fmt.Errorf("gagal membuat direktori uploads: %w", err)
	}

	targetPath := filepath.Join("uploads", filename)
	if err := os.WriteFile(targetPath, cleanBuf.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("gagal menyimpan file gambar: %w", err)
	}

	return filepath.ToSlash(targetPath), nil
}

// ImageDataURI mengubah bytes gambar JPEG atau PNG menjadi data URI base64.
func ImageDataURI(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("data gambar kosong")
	}
	if len(data) > MaxImageUploadBytes {
		return "", errors.New("ukuran gambar melebihi batas 5 MB")
	}

	contentType := http.DetectContentType(data)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return "", errors.New("Hanya menerima gambar JPEG atau PNG")
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", contentType, encoded), nil
}

// ImageFileToDataURI membaca file gambar dari disk dan mengubahnya menjadi data URI.
func ImageFileToDataURI(imagePath string) (string, error) {
	cleanPath := filepath.Clean(imagePath)
	if strings.Contains(cleanPath, "..") {
		return "", errors.New("jalur file tidak valid")
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return "", fmt.Errorf("buka file gambar: %w", err)
	}

	return ImageDataURI(data)
}
