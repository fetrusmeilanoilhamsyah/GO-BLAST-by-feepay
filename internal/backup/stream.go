package backup

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	EntryTypeTable byte = 0x01
	EntryTypeFile  byte = 0x02
	EntryTypeEnd   byte = 0xFF
)

// TableChunkWriter membungkus aliran COPY TO postgres menjadi chunk berpanjang 4-byte uint32.
type TableChunkWriter struct {
	w io.Writer
}

func (tcw *TableChunkWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(p)))
	if _, err := tcw.w.Write(lenBuf[:]); err != nil {
		return 0, err
	}
	n, err := tcw.w.Write(p)
	return n, err
}

func (tcw *TableChunkWriter) Close() error {
	// Tulis panjang 0 sebagai penanda akhir data COPY untuk tabel ini
	var zero [4]byte
	_, err := tcw.w.Write(zero[:])
	return err
}

// TableChunkReader membaca kembali aliran data COPY per tabel hingga menemukan panjang 0.
type TableChunkReader struct {
	r         io.Reader
	currChunk []byte
	offset    int
	finished  bool
}

func (tcr *TableChunkReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for tcr.offset >= len(tcr.currChunk) {
		if tcr.finished {
			return 0, io.EOF
		}

		var lenBuf [4]byte
		if _, err := io.ReadFull(tcr.r, lenBuf[:]); err != nil {
			return 0, fmt.Errorf("gagal membaca panjang chunk tabel: %w", err)
		}

		chunkLen := binary.BigEndian.Uint32(lenBuf[:])
		if chunkLen == 0 {
			tcr.finished = true
			return 0, io.EOF
		}

		tcr.currChunk = make([]byte, chunkLen)
		if _, err := io.ReadFull(tcr.r, tcr.currChunk); err != nil {
			return 0, fmt.Errorf("gagal membaca data chunk tabel (%d byte): %w", chunkLen, err)
		}
		tcr.offset = 0
	}

	n := copy(p, tcr.currChunk[tcr.offset:])
	tcr.offset += n
	return n, nil
}

// StreamWriter mengelola penulisan entri tabel dan file ke dalam aliran data unencrypted.
type StreamWriter struct {
	w io.Writer
}

func NewStreamWriter(w io.Writer) *StreamWriter {
	return &StreamWriter{w: w}
}

// BeginTable menulis header entri tabel dan mengembalikan writer untuk streaming data COPY.
func (sw *StreamWriter) BeginTable(name string, rowCount int64, cols []string) (*TableChunkWriter, error) {
	// 1 byte tipe
	if _, err := sw.w.Write([]byte{EntryTypeTable}); err != nil {
		return nil, err
	}

	// Nama tabel
	nameBytes := []byte(name)
	var nameLen [2]byte
	binary.BigEndian.PutUint16(nameLen[:], uint16(len(nameBytes)))
	if _, err := sw.w.Write(nameLen[:]); err != nil {
		return nil, err
	}
	if _, err := sw.w.Write(nameBytes); err != nil {
		return nil, err
	}

	// Row count
	var rcBuf [8]byte
	binary.BigEndian.PutUint64(rcBuf[:], uint64(rowCount))
	if _, err := sw.w.Write(rcBuf[:]); err != nil {
		return nil, err
	}

	// Kolom
	var colCountBuf [2]byte
	binary.BigEndian.PutUint16(colCountBuf[:], uint16(len(cols)))
	if _, err := sw.w.Write(colCountBuf[:]); err != nil {
		return nil, err
	}
	for _, col := range cols {
		colBytes := []byte(col)
		var clBuf [2]byte
		binary.BigEndian.PutUint16(clBuf[:], uint16(len(colBytes)))
		if _, err := sw.w.Write(clBuf[:]); err != nil {
			return nil, err
		}
		if _, err := sw.w.Write(colBytes); err != nil {
			return nil, err
		}
	}

	return &TableChunkWriter{w: sw.w}, nil
}

// WriteFile menulis entri berkas (misalnya dari folder uploads/) ke stream.
func (sw *StreamWriter) WriteFile(relPath string, size int64, r io.Reader) error {
	if _, err := sw.w.Write([]byte{EntryTypeFile}); err != nil {
		return err
	}

	pathBytes := []byte(relPath)
	var pathLen [2]byte
	binary.BigEndian.PutUint16(pathLen[:], uint16(len(pathBytes)))
	if _, err := sw.w.Write(pathLen[:]); err != nil {
		return err
	}
	if _, err := sw.w.Write(pathBytes); err != nil {
		return err
	}

	var sizeBuf [8]byte
	binary.BigEndian.PutUint64(sizeBuf[:], uint64(size))
	if _, err := sw.w.Write(sizeBuf[:]); err != nil {
		return err
	}

	if size > 0 {
		written, err := io.Copy(sw.w, io.LimitReader(r, size))
		if err != nil {
			return fmt.Errorf("gagal menulis isi berkas %s: %w", relPath, err)
		}
		if written != size {
			return fmt.Errorf("ukuran berkas %s tidak sesuai (diharapkan %d, tertulis %d)", relPath, size, written)
		}
	}
	return nil
}

// Finish menulis penanda akhir stream.
func (sw *StreamWriter) Finish() error {
	_, err := sw.w.Write([]byte{EntryTypeEnd})
	return err
}

// TableHeader berisi metadata tabel saat membaca stream.
type TableHeader struct {
	Name     string
	RowCount int64
	Columns  []string
}

// FileHeader berisi metadata file saat membaca stream.
type FileHeader struct {
	RelPath string
	Size    int64
}

// StreamReader mengelola pembacaan entri tabel dan file dari aliran data yang telah didekripsi.
type StreamReader struct {
	r io.Reader
}

func NewStreamReader(r io.Reader) *StreamReader {
	return &StreamReader{r: r}
}

// Next mengembalikan tipe entri berikutnya: EntryTypeTable, EntryTypeFile, atau EntryTypeEnd.
func (sr *StreamReader) Next() (byte, error) {
	var typeBuf [1]byte
	if _, err := io.ReadFull(sr.r, typeBuf[:]); err != nil {
		return 0, err
	}
	return typeBuf[0], nil
}

// ReadTableHeader membaca informasi tabel yang sedang aktif.
func (sr *StreamReader) ReadTableHeader() (*TableHeader, *TableChunkReader, error) {
	var nameLenBuf [2]byte
	if _, err := io.ReadFull(sr.r, nameLenBuf[:]); err != nil {
		return nil, nil, err
	}
	nameLen := binary.BigEndian.Uint16(nameLenBuf[:])
	nameBytes := make([]byte, nameLen)
	if _, err := io.ReadFull(sr.r, nameBytes); err != nil {
		return nil, nil, err
	}

	var rcBuf [8]byte
	if _, err := io.ReadFull(sr.r, rcBuf[:]); err != nil {
		return nil, nil, err
	}
	rowCount := int64(binary.BigEndian.Uint64(rcBuf[:]))

	var colCountBuf [2]byte
	if _, err := io.ReadFull(sr.r, colCountBuf[:]); err != nil {
		return nil, nil, err
	}
	colCount := int(binary.BigEndian.Uint16(colCountBuf[:]))

	cols := make([]string, colCount)
	for i := 0; i < colCount; i++ {
		var clBuf [2]byte
		if _, err := io.ReadFull(sr.r, clBuf[:]); err != nil {
			return nil, nil, err
		}
		cLen := binary.BigEndian.Uint16(clBuf[:])
		cBytes := make([]byte, cLen)
		if _, err := io.ReadFull(sr.r, cBytes); err != nil {
			return nil, nil, err
		}
		cols[i] = string(cBytes)
	}

	return &TableHeader{
		Name:     string(nameBytes),
		RowCount: rowCount,
		Columns:  cols,
	}, &TableChunkReader{r: sr.r}, nil
}

// ReadFileHeader membaca informasi berkas yang sedang aktif.
func (sr *StreamReader) ReadFileHeader() (*FileHeader, error) {
	var pathLenBuf [2]byte
	if _, err := io.ReadFull(sr.r, pathLenBuf[:]); err != nil {
		return nil, err
	}
	pathLen := binary.BigEndian.Uint16(pathLenBuf[:])
	pathBytes := make([]byte, pathLen)
	if _, err := io.ReadFull(sr.r, pathBytes); err != nil {
		return nil, err
	}

	var sizeBuf [8]byte
	if _, err := io.ReadFull(sr.r, sizeBuf[:]); err != nil {
		return nil, err
	}
	size := int64(binary.BigEndian.Uint64(sizeBuf[:]))

	return &FileHeader{
		RelPath: string(pathBytes),
		Size:    size,
	}, nil
}
