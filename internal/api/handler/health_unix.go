//go:build linux || darwin
// +build linux darwin

package handler

import (
	"sync"
	"syscall"
	"time"
)

// CPU tracking state for calculating delta between polls
var (
	cpuMu           sync.Mutex
	lastCPUTime     time.Duration // user + system time
	lastWallTime    time.Time
	cpuInitialized  bool
)

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

// getCPUUsage returns the CPU usage percentage for this process since last call.
// Uses syscall.Getrusage to get process CPU time and calculates delta.
func getCPUUsage() float64 {
	var rusage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &rusage); err != nil {
		return 0
	}

	// Calculate total CPU time (user + system)
	userTime := time.Duration(rusage.Utime.Sec)*time.Second + time.Duration(rusage.Utime.Usec)*time.Microsecond
	sysTime := time.Duration(rusage.Stime.Sec)*time.Second + time.Duration(rusage.Stime.Usec)*time.Microsecond
	totalCPUTime := userTime + sysTime

	now := time.Now()

	cpuMu.Lock()
	defer cpuMu.Unlock()

	if !cpuInitialized {
		lastCPUTime = totalCPUTime
		lastWallTime = now
		cpuInitialized = true
		return 0 // First call, no delta yet
	}

	// Calculate deltas
	cpuDelta := totalCPUTime - lastCPUTime
	wallDelta := now.Sub(lastWallTime)

	// Update for next call
	lastCPUTime = totalCPUTime
	lastWallTime = now

	// Avoid division by zero
	if wallDelta <= 0 {
		return 0
	}

	// CPU percentage = (CPU time used / wall time elapsed) * 100
	// This gives percentage of a single core. For multi-core, it can exceed 100%.
	// We cap it at 100% for simplicity (single-core equivalent view).
	pct := float64(cpuDelta) / float64(wallDelta) * 100
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}

	return pct
}
