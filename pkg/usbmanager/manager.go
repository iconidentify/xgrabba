package usbmanager

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Manager handles USB drive detection, mounting, and formatting.
type Manager struct {
	exportBasePath string
	logger         *slog.Logger
	mu             sync.RWMutex
	drives         map[string]*Drive // keyed by device path
}

// NewManager creates a new USB manager instance.
func NewManager(exportBasePath string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		exportBasePath: exportBasePath,
		logger:         logger,
		drives:         make(map[string]*Drive),
	}
}

// ScanDrives scans for connected USB drives.
func (m *Manager) ScanDrives(ctx context.Context) ([]Drive, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.drives = make(map[string]*Drive)

	// Scan /sys/block for block devices
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, fmt.Errorf("failed to read /sys/block: %w", err)
	}

	var drives []Drive

	for _, entry := range entries {
		// Skip non-sd devices (we want sda, sdb, etc. which are typically USB)
		name := entry.Name()
		if !strings.HasPrefix(name, "sd") {
			continue
		}

		device := filepath.Join("/dev", name)
		sysPath := filepath.Join("/sys/block", name)

		// Check if it's a removable device
		removable, err := m.isRemovable(sysPath)
		if err != nil || !removable {
			continue
		}

		// Get device info
		drive, err := m.getDriveInfo(device, sysPath)
		if err != nil {
			m.logger.Warn("failed to get drive info", "device", device, "error", err)
			continue
		}

		m.drives[device] = drive
		drives = append(drives, *drive)
	}

	return drives, nil
}

// isRemovable checks if a block device is removable.
func (m *Manager) isRemovable(sysPath string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(sysPath, "removable"))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(data)) == "1", nil
}

// getDriveInfo gathers information about a drive.
func (m *Manager) getDriveInfo(device, sysPath string) (*Drive, error) {
	drive := &Drive{
		Device: device,
	}

	// Get size in bytes
	sizeData, err := os.ReadFile(filepath.Join(sysPath, "size"))
	if err == nil {
		sectors, _ := strconv.ParseInt(strings.TrimSpace(string(sizeData)), 10, 64)
		drive.SizeBytes = sectors * 512 // Standard sector size
	}

	// Get vendor
	vendorData, err := os.ReadFile(filepath.Join(sysPath, "device", "vendor"))
	if err == nil {
		drive.Vendor = strings.TrimSpace(string(vendorData))
	}

	// Get model
	modelData, err := os.ReadFile(filepath.Join(sysPath, "device", "model"))
	if err == nil {
		drive.Model = strings.TrimSpace(string(modelData))
	}

	// Find first partition (e.g., sdb1)
	partitions, _ := filepath.Glob(device + "[0-9]*")
	if len(partitions) > 0 {
		drive.Partition = partitions[0]
	} else {
		drive.Partition = device // Use whole device if no partitions
	}

	// Get filesystem info using blkid
	if fsInfo, err := m.getFilesystemInfo(drive.Partition); err == nil {
		drive.Label = fsInfo.label
		drive.Filesystem = fsInfo.fsType
	}

	// Check mount status
	mountPoint, mounted := m.getMountPoint(drive.Partition)
	drive.IsMounted = mounted
	drive.MountPoint = mountPoint

	// Get space info if mounted
	if mounted {
		m.updateSpaceInfo(drive)
	}

	return drive, nil
}

type fsInfo struct {
	label  string
	fsType string
}

// getFilesystemInfo uses blkid to get filesystem information.
func (m *Manager) getFilesystemInfo(device string) (*fsInfo, error) {
	cmd := exec.Command("blkid", "-o", "export", device)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	info := &fsInfo{}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "LABEL=") {
			// blkid escapes special chars with backslashes (e.g., "STORE\ N\ GO")
			info.label = unescapeShellString(strings.TrimPrefix(line, "LABEL="))
		} else if strings.HasPrefix(line, "TYPE=") {
			info.fsType = strings.TrimPrefix(line, "TYPE=")
		}
	}

	return info, nil
}

// unescapeShellString removes shell-style backslash escapes.
func unescapeShellString(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			result.WriteByte(s[i])
		} else {
			result.WriteByte(s[i])
		}
	}
	return result.String()
}

// getMountPoint checks if a device is mounted and returns its mount point.
func (m *Manager) getMountPoint(device string) (string, bool) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "", false
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == device {
			return unescapeMountPath(fields[1]), true
		}
	}

	return "", false
}

// unescapeMountPath decodes octal escape sequences from /proc/mounts.
// /proc/mounts escapes spaces as \040, tabs as \011, newlines as \012, backslashes as \134.
func unescapeMountPath(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if i+3 < len(s) && s[i] == '\\' && s[i+1] >= '0' && s[i+1] <= '3' {
			if octal, err := strconv.ParseInt(s[i+1:i+4], 8, 32); err == nil {
				result.WriteByte(byte(octal))
				i += 3
				continue
			}
		}
		result.WriteByte(s[i])
	}
	return result.String()
}

// updateSpaceInfo updates free/used space for a mounted drive.
func (m *Manager) updateSpaceInfo(drive *Drive) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(drive.MountPoint, &stat); err != nil {
		return
	}

	drive.FreeBytes = int64(stat.Bavail) * int64(stat.Bsize)
	drive.UsedBytes = drive.SizeBytes - drive.FreeBytes
}

// Mount mounts a USB drive to the export base path.
func (m *Manager) Mount(ctx context.Context, device string, mountAs string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the drive
	drive, ok := m.drives[device]
	if !ok {
		// Rescan to make sure we have the latest
		m.mu.Unlock()
		_, _ = m.ScanDrives(ctx)
		m.mu.Lock()
		drive, ok = m.drives[device]
		if !ok {
			return "", fmt.Errorf("device not found: %s", device)
		}
	}

	if drive.IsMounted {
		return drive.MountPoint, nil // Already mounted
	}

	// Determine mount point name
	name := mountAs
	if name == "" {
		name = drive.Label
	}
	if name == "" {
		name = filepath.Base(device)
	}

	mountPoint := filepath.Join(m.exportBasePath, name)

	// Create mount point directory
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return "", fmt.Errorf("failed to create mount point: %w", err)
	}

	// Mount the partition
	partition := drive.Partition
	if partition == "" {
		partition = device
	}

	cmd := exec.CommandContext(ctx, "mount", "-t", "auto", partition, mountPoint)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("mount failed: %s: %w", string(output), err)
	}

	drive.MountPoint = mountPoint
	drive.IsMounted = true
	m.updateSpaceInfo(drive)

	m.logger.Info("mounted drive", "device", device, "mount_point", mountPoint)
	return mountPoint, nil
}

// Unmount safely unmounts a USB drive.
func (m *Manager) Unmount(ctx context.Context, device string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	drive, ok := m.drives[device]
	if !ok {
		return fmt.Errorf("device not found: %s", device)
	}

	if !drive.IsMounted {
		return nil // Already unmounted
	}

	// Sync first to flush buffers
	cmd := exec.CommandContext(ctx, "sync")
	_ = cmd.Run()

	// Unmount
	cmd = exec.CommandContext(ctx, "umount", drive.MountPoint)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("unmount failed: %s: %w", string(output), err)
	}

	// Remove mount point directory
	_ = os.Remove(drive.MountPoint)

	drive.MountPoint = ""
	drive.IsMounted = false

	m.logger.Info("unmounted drive", "device", device)
	return nil
}

// Format formats a USB drive with the specified filesystem.
func (m *Manager) Format(ctx context.Context, device, fsType, label, confirmToken string) error {
	// Validate confirmation token
	expectedToken := fmt.Sprintf("erase-all-data-on-%s", filepath.Base(device))
	if confirmToken != expectedToken {
		return errors.New("invalid confirmation token")
	}

	// Validate filesystem type
	validFS := map[string]string{
		"exfat": "mkfs.exfat",
		"ext4":  "mkfs.ext4",
		"ntfs":  "mkfs.ntfs",
	}
	mkfsCmd, ok := validFS[strings.ToLower(fsType)]
	if !ok {
		return fmt.Errorf("unsupported filesystem: %s", fsType)
	}

	m.mu.Lock()
	drive, ok := m.drives[device]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("device not found: %s", device)
	}

	// Unmount if mounted
	if drive.IsMounted {
		if err := m.Unmount(ctx, device); err != nil {
			return fmt.Errorf("failed to unmount before format: %w", err)
		}
	}

	// Use the partition if available, otherwise the whole device
	target := drive.Partition
	if target == "" {
		target = device
	}

	m.logger.Info("formatting drive", "device", target, "filesystem", fsType, "label", label)

	// Build format command based on filesystem type
	var cmd *exec.Cmd
	switch fsType {
	case "exfat":
		cmd = exec.CommandContext(ctx, mkfsCmd, "-n", label, target)
	case "ext4":
		cmd = exec.CommandContext(ctx, mkfsCmd, "-L", label, "-F", target)
	case "ntfs":
		cmd = exec.CommandContext(ctx, mkfsCmd, "-f", "-L", label, target)
	default:
		cmd = exec.CommandContext(ctx, mkfsCmd, target)
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("format failed: %s: %w", string(output), err)
	}

	// Update drive info
	m.mu.Lock()
	drive.Label = label
	drive.Filesystem = fsType
	m.mu.Unlock()

	m.logger.Info("formatted drive", "device", target, "filesystem", fsType, "label", label)
	return nil
}

// GetDrive returns information about a specific drive.
func (m *Manager) GetDrive(device string) (*Drive, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	drive, ok := m.drives[device]
	if !ok {
		return nil, fmt.Errorf("device not found: %s", device)
	}

	// Return a copy
	d := *drive
	return &d, nil
}

// ExportBasePath returns the base path for USB exports.
func (m *Manager) ExportBasePath() string {
	return m.exportBasePath
}

// Rename changes the filesystem label of a drive.
func (m *Manager) Rename(ctx context.Context, device, newLabel string) error {
	m.mu.RLock()
	drive, ok := m.drives[device]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("device not found: %s", device)
	}

	partition := drive.Partition
	if partition == "" {
		partition = device
	}

	// Get filesystem type
	fsInfo, err := m.getFilesystemInfo(partition)
	if err != nil {
		return fmt.Errorf("failed to detect filesystem: %w", err)
	}

	// Need to unmount temporarily for some label tools
	wasMounted := drive.IsMounted
	if wasMounted {
		if err := m.Unmount(ctx, device); err != nil {
			return fmt.Errorf("failed to unmount for rename: %w", err)
		}
	}

	// Run appropriate label command based on filesystem
	var cmd *exec.Cmd
	switch fsInfo.fsType {
	case "exfat":
		cmd = exec.CommandContext(ctx, "exfatlabel", partition, newLabel)
	case "ext4", "ext3", "ext2":
		cmd = exec.CommandContext(ctx, "e2label", partition, newLabel)
	case "ntfs":
		cmd = exec.CommandContext(ctx, "ntfslabel", partition, newLabel)
	case "vfat", "fat32", "fat16", "msdos":
		cmd = exec.CommandContext(ctx, "fatlabel", partition, newLabel)
	default:
		// Remount before returning error
		if wasMounted {
			_, _ = m.Mount(ctx, device, "")
		}
		return fmt.Errorf("unsupported filesystem for rename: %s", fsInfo.fsType)
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		// Try to remount even if rename failed
		if wasMounted {
			_, _ = m.Mount(ctx, device, "")
		}
		return fmt.Errorf("rename failed: %s: %w", string(output), err)
	}

	// Update cached drive info
	m.mu.Lock()
	drive.Label = newLabel
	m.mu.Unlock()

	// Remount if it was mounted
	if wasMounted {
		if _, err := m.Mount(ctx, device, ""); err != nil {
			m.logger.Warn("failed to remount after rename", "error", err)
		}
	}

	m.logger.Info("renamed drive", "device", device, "new_label", newLabel)
	return nil
}
