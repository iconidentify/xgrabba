//go:build windows
// +build windows

package handler

// getDiskStats returns disk usage statistics for the given path.
// On Windows, this is a stub that returns zeros.
func getDiskStats(path string) (total, free, used int64, usedPct float64) {
	// Windows disk stats not implemented - runs in Linux containers anyway
	return 0, 0, 0, 0
}

// getCPUUsage returns the CPU usage percentage for this process.
// On Windows, this is a stub that returns zero.
func getCPUUsage() float64 {
	// Windows CPU stats not implemented - runs in Linux containers anyway
	return 0
}
