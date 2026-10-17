package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
)

// ChunkWriter mengalirkan plaintext, memotongnya menjadi chunk 64KB, dan mengenkripsi
// setiap potongan dengan AES-256-GCM ke writer output.
type ChunkWriter struct {
	w            io.Writer
	aead         cipher.AEAD
	headerHash   [32]byte
	chunkIndex   uint64
	randomPrefix [4]byte
	buf          []byte
	hasher       hash.Hash // Menghitung SHA-256 dari seluruh plaintext data stream
	closed       bool
}

// NewChunkWriter membuat ChunkWriter baru dengan kunci 32-byte dan hash kepala.
func NewChunkWriter(w io.Writer, key []byte, headerHash [32]byte) (*ChunkWriter, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("kunci AES-256 harus tepat 32 byte")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat AES cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat GCM cipher: %w", err)
	}

	var randomPrefix [4]byte
	if _, err := io.ReadFull(rand.Reader, randomPrefix[:]); err != nil {
		return nil, fmt.Errorf("gagal menghasilkan random prefix nonce: %w", err)
	}

	return &ChunkWriter{
		w:            w,
		aead:         aead,
		headerHash:   headerHash,
		randomPrefix: randomPrefix,
		buf:          make([]byte, 0, ChunkSize),
		hasher:       sha256.New(),
	}, nil
}

// Write mengonsumsi data plaintext, mengumpulkannya ke buffer hingga ChunkSize,
// dan mengenkripsi serta menulis potongan normal ke writer.
func (cw *ChunkWriter) Write(p []byte) (int, error) {
	if cw.closed {
		return 0, fmt.Errorf("chunk writer sudah ditutup")
	}

	cw.hasher.Write(p)
	total := len(p)

	for len(p) > 0 {
		space := ChunkSize - len(cw.buf)
		if space > len(p) {
			space = len(p)
		}
		cw.buf = append(cw.buf, p[:space]...)
		p = p[space:]

		if len(cw.buf) == ChunkSize {
			if err := cw.flushChunk(false, nil); err != nil {
				return 0, err
			}
		}
	}

	return total, nil
}

// Close menulis potongan terakhir (membawa penanda akhir isLast=0x01, trailer MagicTrailer,
// dan total SHA-256 dari seluruh plaintext stream).
func (cw *ChunkWriter) Close() error {
	if cw.closed {
		return nil
	}
	cw.closed = true

	// Hitung total SHA-256 dari seluruh plaintext stream
	totalHash := cw.hasher.Sum(nil)

	// Buat trailer penutup: MagicTrailer (8 byte) + total SHA-256 (32 byte)
	trailer := make([]byte, 8+32)
	copy(trailer[:8], []byte(MagicTrailer))
	copy(trailer[8:], totalHash)

	return cw.flushChunk(true, trailer)
}

func (cw *ChunkWriter) flushChunk(isLast bool, extraTrailer []byte) error {
	plaintext := cw.buf
	if len(extraTrailer) > 0 {
		plaintext = append(plaintext, extraTrailer...)
	}

	// 1. Susun Nonce 12-byte: 4 byte randomPrefix + 8 byte chunkIndex
	var nonce [12]byte
	copy(nonce[:4], cw.randomPrefix[:])
	binary.BigEndian.PutUint64(nonce[4:], cw.chunkIndex)

	// 2. Susun AAD 41-byte: 32 byte headerHash + 8 byte chunkIndex + 1 byte isLast
	var aad [41]byte
	copy(aad[:32], cw.headerHash[:])
	binary.BigEndian.PutUint64(aad[32:40], cw.chunkIndex)
	if isLast {
		aad[40] = 0x01
	} else {
		aad[40] = 0x00
	}

	// 3. Enkripsi dengan AES-256-GCM
	ciphertext := cw.aead.Seal(nil, nonce[:], plaintext, aad[:])

	// 4. Tulis ke output: Nonce (12 byte) + Panjang Ciphertext (4 byte) + Ciphertext
	if _, err := cw.w.Write(nonce[:]); err != nil {
		return fmt.Errorf("gagal menulis nonce chunk %d: %w", cw.chunkIndex, err)
	}

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ciphertext)))
	if _, err := cw.w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("gagal menulis panjang chunk %d: %w", cw.chunkIndex, err)
	}

	if _, err := cw.w.Write(ciphertext); err != nil {
		return fmt.Errorf("gagal menulis ciphertext chunk %d: %w", cw.chunkIndex, err)
	}

	cw.chunkIndex++
	cw.buf = cw.buf[:0]
	return nil
}
