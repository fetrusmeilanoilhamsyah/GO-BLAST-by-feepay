package backup

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// ReadHeader membaca dan memvalidasi magic serta header JSON dari berkas .gbak tanpa membaca isi data.
func ReadHeader(r io.Reader) (*Header, [32]byte, error) {
	var magicBuf [8]byte
	if _, err := io.ReadFull(r, magicBuf[:]); err != nil {
		return nil, [32]byte{}, fmt.Errorf("gagal membaca magic berkas cadangan: %w", err)
	}
	if string(magicBuf[:]) != Magic {
		return nil, [32]byte{}, fmt.Errorf("berkas bukan cadangan GO BLAST yang valid (magic tidak cocok: %q)", string(magicBuf[:]))
	}

	var hLenBuf [4]byte
	if _, err := io.ReadFull(r, hLenBuf[:]); err != nil {
		return nil, [32]byte{}, fmt.Errorf("gagal membaca panjang header: %w", err)
	}
	hLen := binary.BigEndian.Uint32(hLenBuf[:])
	if hLen == 0 || hLen > 1024*1024 {
		return nil, [32]byte{}, fmt.Errorf("panjang header tidak masuk akal (%d byte)", hLen)
	}

	headerJSON := make([]byte, hLen)
	if _, err := io.ReadFull(r, headerJSON); err != nil {
		return nil, [32]byte{}, fmt.Errorf("gagal membaca JSON header: %w", err)
	}

	var header Header
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, [32]byte{}, fmt.Errorf("gagal unmarshal header JSON: %w", err)
	}

	if header.FormatVersion != FormatVersion {
		return nil, [32]byte{}, fmt.Errorf("versi format cadangan tidak didukung: %d (sistem ini versi %d)", header.FormatVersion, FormatVersion)
	}

	headerHash := sha256.Sum256(headerJSON)
	return &header, headerHash, nil
}

// Verify mendekripsi seluruh berkas cadangan dan memverifikasi setiap potongan AES-256-GCM,
// penanda akhir, checksum total SHA-256, serta kelengkapan seluruh tabel dan baris terhadap header,
// tanpa melakukan penulisan ke database.
func Verify(filePath string, keys [][]byte) (*Header, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka berkas cadangan %s: %w", filePath, err)
	}
	defer file.Close()

	header, headerHash, err := ReadHeader(file)
	if err != nil {
		return nil, err
	}

	// Cari kunci yang cocok berdasarkan KeyID atau coba seluruh kandidat kunci
	var matchingKey []byte
	for _, k := range keys {
		if len(k) == 32 && ComputeKeyID(k) == header.KeyID {
			matchingKey = k
			break
		}
	}
	if matchingKey == nil && len(keys) > 0 {
		for _, k := range keys {
			if len(k) == 32 {
				matchingKey = k
				break
			}
		}
	}
	if len(matchingKey) != 32 {
		return nil, fmt.Errorf("kunci dekripsi cadangan tidak tersedia atau tidak cocok (Key ID: %s)", header.KeyID)
	}

	// Posisi stream sekarang berada tepat di awal potongan terenkripsi
	chunkReader, err := NewChunkReader(file, matchingKey, headerHash)
	if err != nil {
		return nil, fmt.Errorf("gagal menginisialisasi ChunkReader: %w", err)
	}

	gzipReader, err := gzip.NewReader(chunkReader)
	if err != nil {
		return nil, fmt.Errorf("gagal menginisialisasi pembaca gzip (kemungkinan kunci salah atau data rusak): %w", err)
	}
	defer gzipReader.Close()

	streamReader := NewStreamReader(gzipReader)

	// Petakan tabel yang ada di header untuk verifikasi
	expectedTables := make(map[string]int64)
	for _, tm := range header.Tables {
		expectedTables[tm.Name] = tm.RowCount
	}

	actualTablesFound := make(map[string]int64)
	filesCount := 0

	for {
		entryType, err := streamReader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("gagal membaca entri stream: %w", err)
		}

		switch entryType {
		case EntryTypeTable:
			tblHeader, chunkReader, err := streamReader.ReadTableHeader()
			if err != nil {
				return nil, fmt.Errorf("gagal membaca header tabel: %w", err)
			}

			// Hitung baris data COPY (baris dipisahkan oleh newline \n)
			var rowCount int64
			buf := make([]byte, 32*1024)
			for {
				n, rErr := chunkReader.Read(buf)
				if n > 0 {
					rowCount += int64(bytes.Count(buf[:n], []byte("\n")))
				}
				if rErr != nil {
					if rErr == io.EOF {
						break
					}
					return nil, fmt.Errorf("gagal membaca data tabel %s: %w", tblHeader.Name, rErr)
				}
			}

			if expCount, ok := expectedTables[tblHeader.Name]; ok {
				if expCount != tblHeader.RowCount {
					return nil, fmt.Errorf("inkonsistensi baris tabel %s: header mencatat %d, stream mencatat %d", tblHeader.Name, expCount, tblHeader.RowCount)
				}
				if expCount != rowCount {
					return nil, fmt.Errorf("jumlah baris aktual tabel %s (%d) tidak cocok dengan header (%d)", tblHeader.Name, rowCount, expCount)
				}
			} else {
				return nil, fmt.Errorf("tabel %s ditemukan di stream tetapi tidak ada di daftar header", tblHeader.Name)
			}
			actualTablesFound[tblHeader.Name] = rowCount

		case EntryTypeFile:
			fileHeader, err := streamReader.ReadFileHeader()
			if err != nil {
				return nil, fmt.Errorf("gagal membaca header file: %w", err)
			}
			written, err := io.Copy(io.Discard, io.LimitReader(gzipReader, fileHeader.Size))
			if err != nil {
				return nil, fmt.Errorf("gagal memverifikasi berkas %s: %w", fileHeader.RelPath, err)
			}
			if written != fileHeader.Size {
				return nil, fmt.Errorf("ukuran berkas %s tidak sesuai (diharapkan %d, terbaca %d)", fileHeader.RelPath, fileHeader.Size, written)
			}
			filesCount++

		case EntryTypeEnd:
			break

		default:
			return nil, fmt.Errorf("tipe entri stream tidak dikenal (0x%02x)", entryType)
		}

		if entryType == EntryTypeEnd {
			break
		}
	}

	// Pastikan semua tabel dari header ditemukan di stream
	for name, expCount := range expectedTables {
		if actCount, ok := actualTablesFound[name]; !ok || actCount != expCount {
			return nil, fmt.Errorf("tabel %s tidak ditemukan secara utuh di stream cadangan", name)
		}
	}

	if filesCount != header.UploadsCount {
		return nil, fmt.Errorf("jumlah berkas uploads tidak cocok (header %d, stream %d)", header.UploadsCount, filesCount)
	}

	return header, nil
}
