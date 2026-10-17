//go:build !windows

package backup

import (
	"syscall"
)

// GetFreeDiskSpace membaca sisa ruang disk bebas (dalam byte) untuk path yang diberikan pada sistem Unix/Linux.
func GetFreeDiskSpace(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// Bavail adalah blok yang tersedia untuk pengguna non-root
	return stat.Bavail * uint64(stat.Bsize), nil
}
