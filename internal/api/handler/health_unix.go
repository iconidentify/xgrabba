//go:build linux || darwin
// +build linux darwin

package handler

import "syscall"

// getDiskStats returns disk usage statistics for the given path.
func getDiskStats(path string) (total, free, used int64, usedPct float64) {
	var statfs syscall.Statfs_t
	if err := syscall.Statfs(path, &statfs); err == nil {
		total = int64(statfs.Blocks) * int64(statfs.Bsize)
		free = int64(statfs.Bavail) * int64(statfs.Bsize)
		used = total - free
		if total > 0 {
			usedPct = float64(used) / float64(total) * 100
		}
	}
	return
}
