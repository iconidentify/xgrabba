package usbmanager

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FormatOperation tracks an in-progress format operation.
type FormatOperation struct {
	ID         string
	Progress   FormatProgress
	cancelFunc context.CancelFunc
	mu         sync.RWMutex
}

// FormatManager handles async format operations.
type FormatManager struct {
	manager    *Manager
	operations map[string]*FormatOperation
	mu         sync.RWMutex
	logger     *slog.Logger
}

// NewFormatManager creates a new format manager.
func NewFormatManager(manager *Manager, logger *slog.Logger) *FormatManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &FormatManager{
		manager:    manager,
		operations: make(map[string]*FormatOperation),
		logger:     logger,
	}
}

// StartFormat begins an async format operation.
func (fm *FormatManager) StartFormat(device, fsType, label, confirmToken string) (string, error) {
	// Validate confirmation token
	expectedToken := fmt.Sprintf("erase-all-data-on-%s", filepath.Base(device))
	if confirmToken != expectedToken {
		return "", fmt.Errorf("invalid confirmation token")
	}

	// Validate filesystem type
	validFS := map[string]bool{"exfat": true, "ext4": true, "ntfs": true}
	if !validFS[strings.ToLower(fsType)] {
		return "", fmt.Errorf("unsupported filesystem: %s", fsType)
	}

	// Generate operation ID
	opID := fmt.Sprintf("fmt_%d", time.Now().UnixNano())

	ctx, cancel := context.WithCancel(context.Background())

	op := &FormatOperation{
		ID: opID,
		Progress: FormatProgress{
			OperationID: opID,
			Device:      device,
			Phase:       "preparing",
			Progress:    0,
			StartedAt:   time.Now().Unix(),
		},
		cancelFunc: cancel,
	}

	// Get drive info for size estimation
	drive, err := fm.manager.GetDrive(device)
	if err != nil {
		cancel()
		return "", err
	}
	op.Progress.TotalBytes = drive.SizeBytes

	// Calculate estimated time (rough: ~10MB/s for USB 2.0, ~100MB/s for USB 3.0)
	// Conservative estimate using USB 2.0 speed
	op.Progress.EstimatedSecs = int(drive.SizeBytes / (10 * 1024 * 1024))
	if op.Progress.EstimatedSecs < 5 {
		op.Progress.EstimatedSecs = 5
	}

	fm.mu.Lock()
	fm.operations[opID] = op
	fm.mu.Unlock()

	go fm.runFormat(ctx, op, device, fsType, label)

	fm.logger.Info("started async format", "operation_id", opID, "device", device)
	return opID, nil
}

func (fm *FormatManager) runFormat(ctx context.Context, op *FormatOperation, device, fsType, label string) {
	defer func() {
		op.mu.Lock()
		if op.Progress.Phase != "completed" && op.Progress.Phase != "failed" {
			op.Progress.Phase = "failed"
			op.Progress.Error = "unexpected termination"
		}
		op.mu.Unlock()
	}()

	// Phase 1: Unmount if mounted
	op.updatePhase("unmounting", 5)
	drive, _ := fm.manager.GetDrive(device)
	if drive != nil && drive.IsMounted {
		if err := fm.manager.Unmount(ctx, device); err != nil {
			op.setError("unmount failed: " + err.Error())
			return
		}
	}

	// Phase 2: Format
	op.updatePhase("formatting", 10)

	partition := device
	if drive != nil && drive.Partition != "" {
		partition = drive.Partition
	}

	// Determine the mkfs command
	var cmd *exec.Cmd
	switch strings.ToLower(fsType) {
	case "exfat":
		cmd = exec.CommandContext(ctx, "mkfs.exfat", "-n", label, partition)
	case "ext4":
		cmd = exec.CommandContext(ctx, "mkfs.ext4", "-L", label, "-F", partition)
	case "ntfs":
		cmd = exec.CommandContext(ctx, "mkfs.ntfs", "-f", "-L", label, partition)
	default:
		op.setError("unsupported filesystem: " + fsType)
		return
	}

	// Track progress using time estimation
	type formatResult struct {
		err    error
		output string
	}
	done := make(chan formatResult, 1)
	go func() {
		output, err := cmd.CombinedOutput()
		done <- formatResult{err: err, output: string(output)}
	}()

	startTime := time.Now()
	progressTicker := time.NewTicker(500 * time.Millisecond)
	defer progressTicker.Stop()

	for {
		select {
		case result := <-done:
			if result.err != nil {
				errMsg := fmt.Sprintf("format failed: %v", result.err)
				if result.output != "" {
					errMsg += " - " + result.output
				}
				fm.logger.Error("mkfs failed", "device", partition, "error", result.err, "output", result.output)
				op.setError(errMsg)
				return
			}
			fm.logger.Info("mkfs completed", "device", partition, "output", result.output)
			goto verify
		case <-ctx.Done():
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			op.setError("cancelled")
			return
		case <-progressTicker.C:
			elapsed := time.Since(startTime).Seconds()
			estimated := float64(op.Progress.EstimatedSecs)

			// Progress based on time estimation (capped at 85%)
			progress := int((elapsed / estimated) * 75)
			if progress > 85 {
				progress = 85
			}
			op.updateProgress(10 + progress)
			op.mu.Lock()
			op.Progress.ElapsedSecs = int(elapsed)
			op.mu.Unlock()
		}
	}

verify:
	// Phase 3: Verify
	op.updatePhase("verifying", 90)

	// Verify that blkid can read the filesystem and it matches expected type
	verifyCmd := exec.CommandContext(ctx, "blkid", "-o", "value", "-s", "TYPE", partition)
	verifyOutput, err := verifyCmd.Output()
	if err != nil {
		fm.logger.Error("blkid verification failed", "device", partition, "error", err)
		op.setError("verification failed: drive may not be properly formatted")
		return
	}

	actualFS := strings.TrimSpace(string(verifyOutput))
	expectedFS := strings.ToLower(fsType)
	if actualFS != expectedFS {
		fm.logger.Error("filesystem mismatch", "device", partition, "expected", expectedFS, "actual", actualFS)
		op.setError(fmt.Sprintf("verification failed: expected %s but got %s", expectedFS, actualFS))
		return
	}
	fm.logger.Info("filesystem verified", "device", partition, "type", actualFS)

	// Phase 4: Complete
	op.updatePhase("completed", 100)

	// Refresh drive info
	_, _ = fm.manager.ScanDrives(ctx)

	fm.logger.Info("format completed", "operation_id", op.ID, "device", device)
}

func (op *FormatOperation) updatePhase(phase string, progress int) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.Progress.Phase = phase
	op.Progress.Progress = progress
}

func (op *FormatOperation) updateProgress(progress int) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.Progress.Progress = progress
}

func (op *FormatOperation) setError(err string) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.Progress.Phase = "failed"
	op.Progress.Error = err
}

// GetProgress returns the current progress of a format operation.
func (fm *FormatManager) GetProgress(operationID string) (*FormatProgress, error) {
	fm.mu.RLock()
	op, ok := fm.operations[operationID]
	fm.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("operation not found: %s", operationID)
	}

	op.mu.RLock()
	defer op.mu.RUnlock()

	// Return a copy
	progress := op.Progress
	progress.ElapsedSecs = int(time.Since(time.Unix(progress.StartedAt, 0)).Seconds())
	return &progress, nil
}

// CancelFormat cancels an in-progress format operation.
func (fm *FormatManager) CancelFormat(operationID string) error {
	fm.mu.RLock()
	op, ok := fm.operations[operationID]
	fm.mu.RUnlock()

	if !ok {
		return fmt.Errorf("operation not found: %s", operationID)
	}

	if op.cancelFunc != nil {
		op.cancelFunc()
	}

	fm.logger.Info("format cancelled", "operation_id", operationID)
	return nil
}

// CleanupOldOperations removes completed operations older than the given duration.
func (fm *FormatManager) CleanupOldOperations(maxAge time.Duration) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	cutoff := time.Now().Add(-maxAge).Unix()

	for id, op := range fm.operations {
		op.mu.RLock()
		if op.Progress.Phase == "completed" || op.Progress.Phase == "failed" {
			if op.Progress.StartedAt < cutoff {
				delete(fm.operations, id)
			}
		}
		op.mu.RUnlock()
	}
}
