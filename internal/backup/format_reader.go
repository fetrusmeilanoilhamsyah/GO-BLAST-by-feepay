package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
)

// ChunkReader membaca dan mendekripsi potongan AES-256-GCM secara streaming.
type ChunkReader struct {
	r          io.Reader
	aead       cipher.AEAD
	headerHash [32]byte
	chunkIndex uint64
	hasher     hash.Hash // Menghitung SHA-256 dari seluruh plaintext stream yang didekripsi
	buf        []byte
	bufOffset  int
	finished   bool
}

// NewChunkReader membuat ChunkReader baru dengan kunci 32-byte dan hash kepala.
func NewChunkReader(r io.Reader, key []byte, headerHash [32]byte) (*ChunkReader, error) {
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

	return &ChunkReader{
		r:          r,
		aead:       aead,
		headerHash: headerHash,
		hasher:     sha256.New(),
	}, nil
}

// Read mengembalikan byte plaintext hasil dekripsi potongan.
func (cr *ChunkReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for cr.bufOffset >= len(cr.buf) {
		if cr.finished {
			return 0, io.EOF
		}
		if err := cr.readNextChunk(); err != nil {
			return 0, err
		}
	}

	n := copy(p, cr.buf[cr.bufOffset:])
	cr.bufOffset += n
	return n, nil
}

func (cr *ChunkReader) readNextChunk() error {
	// 1. Baca Nonce 12-byte
	var nonce [12]byte
	if _, err := io.ReadFull(cr.r, nonce[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			if !cr.finished {
				return fmt.Errorf("berkas cadangan terpotong: tidak menemukan potongan akhir")
			}
			return io.EOF
		}
		return fmt.Errorf("gagal membaca nonce chunk %d: %w", cr.chunkIndex, err)
	}

	// Verifikasi nomor potongan di nonce
	nonceIndex := binary.BigEndian.Uint64(nonce[4:])
	if nonceIndex != cr.chunkIndex {
		return fmt.Errorf("urutan potongan rusak: diharapkan indeks %d, terbaca %d", cr.chunkIndex, nonceIndex)
	}

	// 2. Baca Panjang Ciphertext 4-byte
	var lenBuf [4]byte
	if _, err := io.ReadFull(cr.r, lenBuf[:]); err != nil {
		return fmt.Errorf("gagal membaca panjang chunk %d: %w", cr.chunkIndex, err)
	}
	cipherLen := binary.BigEndian.Uint32(lenBuf[:])
	if cipherLen < 16 || cipherLen > uint32(ChunkSize+8+32+16+1024) {
		return fmt.Errorf("ukuran chunk %d tidak valid (%d byte)", cr.chunkIndex, cipherLen)
	}

	// 3. Baca Ciphertext
	ciphertext := make([]byte, cipherLen)
	if _, err := io.ReadFull(cr.r, ciphertext); err != nil {
		return fmt.Errorf("gagal membaca ciphertext chunk %d: %w", cr.chunkIndex, err)
	}

	// 4. Dekripsi: coba sebagai chunk normal (isLast=0x00) terlebih dahulu
	var aad [41]byte
	copy(aad[:32], cr.headerHash[:])
	binary.BigEndian.PutUint64(aad[32:40], cr.chunkIndex)
	aad[40] = 0x00

	plaintext, err := cr.aead.Open(nil, nonce[:], ciphertext, aad[:])
	if err == nil {
		// Chunk normal berhasil didekripsi
		cr.hasher.Write(plaintext)
		cr.buf = plaintext
		cr.bufOffset = 0
		cr.chunkIndex++
		return nil
	}

	// 5. Jika gagal, coba sebagai potongan terakhir (isLast=0x01)
	aad[40] = 0x01
	plaintext, err = cr.aead.Open(nil, nonce[:], ciphertext, aad[:])
	if err != nil {
		// Kunci salah, data korup, potongan ditukar, atau AAD tidak cocok
		return fmt.Errorf("dekripsi chunk %d gagal: otentikasi integritas data atau kunci tidak cocok", cr.chunkIndex)
	}

	// Potongan terakhir berhasil didekripsi: harus memuat trailer MagicTrailer (8 byte) + total SHA-256 (32 byte)
	if len(plaintext) < 8+32 {
		return fmt.Errorf("potongan akhir terlalu pendek untuk memuat trailer integritas")
	}

	dataLen := len(plaintext) - (8 + 32)
	dataPayload := plaintext[:dataLen]
	trailer := plaintext[dataLen:]

	if !bytes.Equal(trailer[:8], []byte(MagicTrailer)) {
		return fmt.Errorf("trailer akhir cadangan tidak valid (magic trailer tidak cocok)")
	}
	expectedHash := trailer[8:]

	cr.hasher.Write(dataPayload)
	actualHash := cr.hasher.Sum(nil)

	if !bytes.Equal(actualHash, expectedHash) {
		return fmt.Errorf("checksum SHA-256 stream cadangan tidak cocok (data rusak atau dimanipulasi)")
	}

	cr.buf = dataPayload
	cr.bufOffset = 0
	cr.chunkIndex++
	cr.finished = true
	return nil
}
