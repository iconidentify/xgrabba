//go:build windows

package service

import (
	"os"

	"golang.org/x/sys/windows"
)

func getFreeDiskSpace(path string) int64 {
	stat, err := os.Stat(path)
	if err != nil || !stat.IsDir() {
		return 0
	}

	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}

	var freeBytes, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeBytes, &totalBytes, &totalFreeBytes); err != nil {
		return 0
	}

	return int64(freeBytes)
}
