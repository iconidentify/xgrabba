//go:build !windows

package service

import (
	"os"
	"syscall"
)

func getFreeDiskSpace(path string) int64 {
	stat, err := os.Stat(path)
	if err != nil || !stat.IsDir() {
		return 0
	}

	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0
	}

	return int64(fs.Bavail) * int64(fs.Bsize)
}
