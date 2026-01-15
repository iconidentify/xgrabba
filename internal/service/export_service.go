package service

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/iconidentify/xgrabba/internal/domain"
	"github.com/iconidentify/xgrabba/pkg/crypto"
	"github.com/iconidentify/xgrabba/pkg/ui"
)

// ExportService handles exporting the archive to portable formats.
type ExportService struct {
	tweetSvc      *TweetService
	logger        *slog.Logger
	eventEmitter  domain.EventEmitter
	storagePath   string // Base storage path for persistence
	volumeLister  func() []Volume
	estimateSizer func(ctx context.Context) (int64, error)

	// Async export state
	mu           sync.Mutex
	activeExport *ActiveExport
}

// ActiveExport tracks an in-progress export operation.
type ActiveExport struct {
	ID             string             `json:"export_id"`
	DestPath       string             `json:"dest_path"`
	MountPoint     string             `json:"mount_point,omitempty"` // USB mount point if exporting to USB
	Phase          string             `json:"phase"`                 // preparing, exporting, finalizing, completed, failed, cancelled
	TotalTweets    int                `json:"total_tweets"`
	ExportedTweets int                `json:"exported_tweets"`
	BytesWritten   int64              `json:"bytes_written"`
	CurrentFile    string             `json:"current_file"`
	StartedAt      time.Time          `json:"started_at"`
	Error          string             `json:"error,omitempty"`
	ZipPath        string             `json:"zip_path,omitempty"` // Path to downloadable zip file
	cancelFunc     context.CancelFunc `json:"-"`
}

// ExportEstimate contains size estimates for an export.
type ExportEstimate struct {
	TweetCount         int      `json:"tweet_count"`
	MediaCount         int      `json:"media_count"`
	EstimatedSizeBytes int64    `json:"estimated_size_bytes"`
	Volumes            []Volume `json:"volumes"`
}

// Volume represents an available storage volume.
type Volume struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	FreeBytes int64  `json:"free_bytes"`
}

// NewExportService creates a new export service.
func NewExportService(tweetSvc *TweetService, logger *slog.Logger, eventEmitter domain.EventEmitter, storagePath string) *ExportService {
	svc := &ExportService{
		tweetSvc:     tweetSvc,
		logger:       logger,
		eventEmitter: eventEmitter,
		storagePath:  storagePath,
	}
	svc.volumeLister = svc.GetAvailableVolumes
	svc.estimateSizer = func(ctx context.Context) (int64, error) {
		estimate, err := svc.EstimateExport(ctx)
		if err != nil {
			return 0, err
		}
		return estimate.EstimatedSizeBytes, nil
	}

	// Load persisted export state on startup
	if err := svc.loadExportState(); err != nil {
		logger.Warn("failed to load export state", "error", err)
	}

	return svc
}

const (
	exportSizeSafetyMargin = 0.10
	exportSizeMinHeadroom  = 256 * 1024 * 1024 // 256MB
)

// emitEvent emits an event if the event emitter is configured.
func (s *ExportService) emitEvent(severity domain.EventSeverity, category domain.EventCategory, message string, metadata domain.EventMetadata) {
	if s.eventEmitter == nil {
		return
	}
	s.eventEmitter.Emit(domain.Event{
		Timestamp: time.Now(),
		Severity:  severity,
		Category:  category,
		Message:   message,
		Source:    "ExportService",
		Metadata:  metadata.ToJSON(),
	})
}

// EstimateExport calculates the estimated size and counts for an export.
func (s *ExportService) EstimateExport(ctx context.Context) (*ExportEstimate, error) {
	tweets, _, err := s.tweetSvc.List(ctx, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("list tweets: %w", err)
	}

	var totalSize int64
	var mediaCount int

	for _, tweet := range tweets {
		// Estimate tweet metadata size (~2KB per tweet)
		totalSize += 2048

		// Add media file sizes
		for _, media := range tweet.Media {
			mediaCount++
			if media.LocalPath != "" {
				if stat, err := os.Stat(media.LocalPath); err == nil {
					totalSize += stat.Size()
				}
			}
		}

		// Avatar estimate (~50KB)
		if tweet.Author.LocalAvatarURL != "" {
			totalSize += 50 * 1024
		}
	}

	// Add overhead for index.html (~350KB) and viewers (~50MB if included)
	totalSize += 350 * 1024

	return &ExportEstimate{
		TweetCount:         len(tweets),
		MediaCount:         mediaCount,
		EstimatedSizeBytes: totalSize,
		Volumes:            s.GetAvailableVolumes(),
	}, nil
}

// GetAvailableVolumes returns a list of available storage volumes (USB drives, etc.).
func (s *ExportService) GetAvailableVolumes() []Volume {
	volumes := []Volume{}

	switch runtime.GOOS {
	case "darwin":
		// macOS: Check /Volumes
		entries, err := os.ReadDir("/Volumes")
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != "Macintosh HD" {
					path := filepath.Join("/Volumes", entry.Name())
					free := getFreeDiskSpace(path)
					if free > 0 {
						volumes = append(volumes, Volume{
							Path:      path,
							Name:      entry.Name(),
							FreeBytes: free,
						})
					}
				}
			}
		}
	case "linux":
		// Linux: Check /media and /mnt
		for _, base := range []string{"/media", "/mnt"} {
			entries, err := os.ReadDir(base)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() {
						// Check for user subdirectories in /media
						if base == "/media" {
							subpath := filepath.Join(base, entry.Name())
							subentries, err := os.ReadDir(subpath)
							if err == nil {
								for _, subentry := range subentries {
									if subentry.IsDir() {
										path := filepath.Join(subpath, subentry.Name())
										free := getFreeDiskSpace(path)
										if free > 0 {
											volumes = append(volumes, Volume{
												Path:      path,
												Name:      subentry.Name(),
												FreeBytes: free,
											})
										}
									}
								}
							}
						} else {
							path := filepath.Join(base, entry.Name())
							free := getFreeDiskSpace(path)
							if free > 0 {
								volumes = append(volumes, Volume{
									Path:      path,
									Name:      entry.Name(),
									FreeBytes: free,
								})
							}
						}
					}
				}
			}
		}
	case "windows":
		// Windows: Check drive letters D-Z
		for c := 'D'; c <= 'Z'; c++ {
			path := string(c) + ":\\"
			free := getFreeDiskSpace(path)
			if free > 0 {
				volumes = append(volumes, Volume{
					Path:      path,
					Name:      string(c) + ":",
					FreeBytes: free,
				})
			}
		}
	}

	return volumes
}

// ErrExportInProgress is returned when trying to start an export while one is already running.
var ErrExportInProgress = fmt.Errorf("export already in progress")

// StartExportAsync starts an export operation in the background.
func (s *ExportService) StartExportAsync(opts ExportOptions) (string, error) {
	s.mu.Lock()
	if s.activeExport != nil && (s.activeExport.Phase == "preparing" || s.activeExport.Phase == "exporting" || s.activeExport.Phase == "finalizing") {
		s.mu.Unlock()
		return "", ErrExportInProgress
	}
	s.mu.Unlock()

	// Generate export ID
	exportID := fmt.Sprintf("exp_%d", time.Now().UnixNano())

	// Validate available space before starting
	if err := s.ensureExportSpace(context.Background(), opts); err != nil {
		return "", err
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.activeExport = &ActiveExport{
		ID:         exportID,
		DestPath:   opts.DestPath,
		MountPoint: opts.MountPoint,
		Phase:      "preparing",
		StartedAt:  time.Now(),
		cancelFunc: cancel,
	}
	s.mu.Unlock()

	// Emit export started event
	s.emitEvent(domain.EventSeverityInfo, domain.EventCategoryExport,
		fmt.Sprintf("Export started to %s", opts.DestPath),
		domain.EventMetadata{"export_id": exportID, "dest_path": opts.DestPath, "mount_point": opts.MountPoint, "encrypted": opts.Encrypt})

	// Start export in background
	go s.runExportAsync(ctx, opts)

	return exportID, nil
}

func (s *ExportService) ensureExportSpace(ctx context.Context, opts ExportOptions) error {
	if opts.DestPath == "" {
		return nil
	}

	estimatedBytes, err := s.estimateSizer(ctx)
	if err != nil {
		return fmt.Errorf("estimate export size: %w", err)
	}

	requiredBytes := int64(float64(estimatedBytes)*(1.0+exportSizeSafetyMargin)) + exportSizeMinHeadroom
	volumePath, freeBytes := s.resolveFreeBytes(opts)
	if freeBytes == 0 {
		return fmt.Errorf("unable to determine free space for %s", volumePath)
	}

	if freeBytes < requiredBytes {
		return fmt.Errorf("insufficient space on %s: need %s, have %s",
			volumePath,
			formatExportBytes(requiredBytes),
			formatExportBytes(freeBytes),
		)
	}

	return nil
}

func (s *ExportService) resolveFreeBytes(opts ExportOptions) (string, int64) {
	volumes := s.volumeLister()

	cleanMount := filepath.Clean(opts.MountPoint)
	if cleanMount != "." && cleanMount != "" {
		for _, vol := range volumes {
			if filepath.Clean(vol.Path) == cleanMount {
				return vol.Path, vol.FreeBytes
			}
		}
		if free := getFreeDiskSpace(cleanMount); free > 0 {
			return cleanMount, free
		}
	}

	cleanDest := filepath.Clean(opts.DestPath)
	bestMatch := ""
	bestFree := int64(0)
	for _, vol := range volumes {
		volPath := filepath.Clean(vol.Path)
		if strings.HasPrefix(cleanDest, volPath) && len(volPath) > len(bestMatch) {
			bestMatch = vol.Path
			bestFree = vol.FreeBytes
		}
	}
	if bestMatch != "" {
		return bestMatch, bestFree
	}

	if free := getFreeDiskSpace(cleanDest); free > 0 {
		return cleanDest, free
	}

	parent := filepath.Dir(cleanDest)
	if free := getFreeDiskSpace(parent); free > 0 {
		return parent, free
	}

	return cleanDest, 0
}

func formatExportBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	suffixes := []string{"KB", "MB", "GB", "TB", "PB"}
	if exp >= len(suffixes) {
		exp = len(suffixes) - 1
	}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), suffixes[exp])
}

// runExportAsync runs the export operation and updates progress.
func (s *ExportService) runExportAsync(ctx context.Context, opts ExportOptions) {
	defer func() {
		// Ensure phase is set on exit if not already completed/failed/cancelled
		s.mu.Lock()
		if s.activeExport != nil && s.activeExport.Phase != "completed" && s.activeExport.Phase != "failed" && s.activeExport.Phase != "cancelled" {
			s.activeExport.Phase = "failed"
			s.activeExport.Error = "unexpected exit"
		}
		s.mu.Unlock()
	}()

	// Validate destination
	if opts.DestPath == "" {
		s.setExportError("destination path is required")
		return
	}

	// Sanitize path: remove shell-style backslash escapes (e.g., "\ " -> " ")
	// Users may copy-paste paths from terminal with escapes
	destPath := strings.ReplaceAll(opts.DestPath, "\\ ", " ")
	destPath = strings.ReplaceAll(destPath, "\\(", "(")
	destPath = strings.ReplaceAll(destPath, "\\)", ")")
	destPath = strings.ReplaceAll(destPath, "\\'", "'")

	// Expand ~ to home directory
	if strings.HasPrefix(destPath, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			destPath = filepath.Join(home, destPath[2:])
		}
	}

	// Clean path: remove trailing slashes and normalize
	destPath = filepath.Clean(destPath)
	opts.DestPath = destPath

	s.logger.Info("export destination", "path", opts.DestPath)

	// Check if destination exists - if so, test write access directly on it
	// If not, check parent directory
	var writeTestDir string
	if info, err := os.Stat(opts.DestPath); err == nil && info.IsDir() {
		// Destination exists and is a directory (e.g., USB mount point)
		writeTestDir = opts.DestPath
	} else {
		// Destination doesn't exist, check parent
		parentDir := filepath.Dir(opts.DestPath)
		if info, err := os.Stat(parentDir); err != nil {
			s.setExportError(fmt.Sprintf("parent directory does not exist: %s", parentDir))
			return
		} else if !info.IsDir() {
			s.setExportError(fmt.Sprintf("parent path is not a directory: %s", parentDir))
			return
		}
		writeTestDir = parentDir
	}

	// Test write access
	testFile := filepath.Join(writeTestDir, ".xgrabba_write_test")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		s.setExportError(fmt.Sprintf("no write permission on %s: %v", writeTestDir, err))
		return
	}
	os.Remove(testFile)

	// Check if destination has existing files (require empty/formatted drive)
	// This prevents exporting to a populated drive which could cause confusion
	entries, err := os.ReadDir(opts.DestPath)
	if err == nil && len(entries) > 0 {
		// Check if these are leftover files from a previous xgrabba export
		hasNonXgrabbaFiles := false
		for _, entry := range entries {
			name := entry.Name()
			// Allow only xgrabba export files/dirs
			if name != "data" && name != "index.html" && name != "README.txt" &&
				name != "tweets-data.json" && !strings.HasPrefix(name, "xgrabba-viewer") &&
				!strings.HasPrefix(name, ".") { // Allow hidden files like .Trashes, .Spotlight-V100
				hasNonXgrabbaFiles = true
				break
			}
		}
		if hasNonXgrabbaFiles {
			s.setExportError("destination contains existing files - please format the drive first or use an empty drive")
			return
		}
		// Clean up previous xgrabba export files
		s.logger.Info("cleaning up previous export files", "path", opts.DestPath)
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue // Don't delete hidden system files
			}
			path := filepath.Join(opts.DestPath, name)
			if entry.IsDir() {
				os.RemoveAll(path)
			} else {
				os.Remove(path)
			}
		}
	}

	// Create destination directory (no-op if already exists)
	if err := os.MkdirAll(opts.DestPath, 0755); err != nil {
		s.setExportError(fmt.Sprintf("create destination directory: %v, path: %s", err, opts.DestPath))
		return
	}

	// Get all tweets
	tweets, _, err := s.tweetSvc.List(ctx, 0, 0)
	if err != nil {
		s.setExportError(fmt.Sprintf("list tweets: %v", err))
		return
	}

	// Apply filters
	if opts.DateRange != nil || len(opts.Authors) > 0 || opts.SearchQuery != "" {
		tweets = s.filterTweets(tweets, opts)
	}

	// Sort by date (newest first)
	sort.Slice(tweets, func(i, j int) bool {
		return tweets[i].CreatedAt.After(tweets[j].CreatedAt)
	})

	// Update total count
	s.mu.Lock()
	s.activeExport.TotalTweets = len(tweets)
	s.activeExport.Phase = "exporting"
	s.mu.Unlock()

	// Set up encryption context if encryption is enabled
	// This uses streaming encryption - files are encrypted as they're copied,
	// so unencrypted data never touches the USB drive
	var encCtx *encryptionContext
	if opts.Encrypt && opts.Password != "" {
		s.mu.Lock()
		s.activeExport.CurrentFile = "Deriving encryption key (AES-256 + Argon2id)..."
		exportID := s.activeExport.ID
		s.mu.Unlock()

		s.emitEvent(domain.EventSeverityInfo, domain.EventCategoryEncryption,
			"Setting up streaming encryption with AES-256-GCM",
			domain.EventMetadata{"export_id": exportID, "algorithm": "AES-256-GCM", "kdf": "Argon2id"})

		var err error
		// Skip per-file syncs for USB drives - batch syncs at the end for better performance
		encCtx, err = newEncryptionContext(opts.Password, opts.DestPath, true)
		if err != nil {
			s.setExportError(fmt.Sprintf("setup encryption: %v", err))
			return
		}
		s.logger.Info("streaming encryption initialized", "enc_dir", encCtx.encDir)
	}

	// Create data directory only if not encrypting
	// (encrypted files go directly to encrypted/ directory)
	dataDir := filepath.Join(opts.DestPath, "data")
	if encCtx == nil {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			s.setExportError(fmt.Sprintf("create data directory: %v", err))
			return
		}
	}

	// Export tweets and media
	exportedTweets := make([]ExportedTweet, 0, len(tweets))

	for i, tweet := range tweets {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.activeExport.Phase = "cancelled"
			s.mu.Unlock()
			s.saveExportState() // Persist cancelled state
			// Clean up partial export
			s.logger.Info("export cancelled, cleaning up partial files", "path", opts.DestPath)
			s.cleanupExport(opts.DestPath)
			return
		default:
		}

		// Update progress
		s.mu.Lock()
		s.activeExport.ExportedTweets = i
		if encCtx != nil {
			s.activeExport.CurrentFile = fmt.Sprintf("Encrypting: %s (@%s)", tweet.AITitle, tweet.Author.Username)
		} else {
			s.activeExport.CurrentFile = fmt.Sprintf("%s (@%s)", tweet.AITitle, tweet.Author.Username)
		}
		s.mu.Unlock()

		exported, size, _, err := s.exportTweet(ctx, tweet, dataDir, encCtx)
		if err != nil {
			s.logger.Warn("failed to export tweet", "tweet_id", tweet.ID, "error", err)
			continue
		}

		exportedTweets = append(exportedTweets, *exported)

		s.mu.Lock()
		s.activeExport.BytesWritten += size
		s.mu.Unlock()
	}

	// Update phase to finalizing
	s.mu.Lock()
	s.activeExport.Phase = "finalizing"
	s.activeExport.ExportedTweets = len(exportedTweets)
	s.activeExport.CurrentFile = "Writing metadata..."
	s.mu.Unlock()

	// Count media files
	mediaCount := 0
	for _, t := range exportedTweets {
		mediaCount += len(t.Media)
	}

	// Build tweets data structure
	exportedAt := time.Now().UTC()
	tweetsData := map[string]interface{}{
		"tweets":      exportedTweets,
		"total":       len(exportedTweets),
		"exported_at": exportedAt,
		"version":     "1.0",
		"metadata": map[string]interface{}{
			"tweet_count":    len(exportedTweets),
			"media_count":    mediaCount,
			"encrypted":      opts.Encrypt,
			"export_id":      s.activeExport.ID,
			"bytes_written":  s.activeExport.BytesWritten,
			"export_version": "2.0",
			"exported_at":    exportedAt.Format(time.RFC3339),
			"exported_date":  exportedAt.Format("January 2, 2006"),
			"exported_time":  exportedAt.Format("3:04 PM MST"),
		},
	}

	tweetsJSON, err := json.MarshalIndent(tweetsData, "", "  ")
	if err != nil {
		s.setExportError(fmt.Sprintf("marshal tweets data: %v", err))
		return
	}

	// Handle encrypted vs non-encrypted finalization differently
	if encCtx != nil {
		// Streaming encryption: write encrypted manifest and data
		s.mu.Lock()
		s.activeExport.CurrentFile = "Syncing encrypted files to disk..."
		exportID := s.activeExport.ID
		s.mu.Unlock()

		// Batch sync all encrypted files (much faster than syncing after each file)
		// Check for cancellation before batch sync
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.activeExport.Phase = "cancelled"
			s.mu.Unlock()
			s.logger.Info("export cancelled before batch sync", "path", opts.DestPath)
			s.cleanupExport(opts.DestPath)
			return
		default:
		}

		s.logger.Info("batch syncing encrypted files", "file_count", len(encCtx.syncFiles))
		syncStart := time.Now()
		if err := encCtx.batchSyncFiles(ctx); err != nil {
			if err == context.Canceled {
				s.logger.Info("batch sync cancelled", "path", opts.DestPath)
				s.cleanupExport(opts.DestPath)
				return
			}
			s.logger.Warn("batch sync had errors", "error", err)
			// Continue anyway - files are written, just not synced
		} else {
			syncDuration := time.Since(syncStart)
			s.logger.Info("batch sync completed", "file_count", len(encCtx.syncFiles), "duration", syncDuration)
		}

		s.mu.Lock()
		s.activeExport.CurrentFile = "Writing encrypted metadata..."
		s.mu.Unlock()

		if err := encCtx.writeManifestAndData(opts.DestPath, tweetsJSON); err != nil {
			s.setExportError(fmt.Sprintf("write encrypted data: %v", err))
			return
		}

		s.logger.Info("encrypted data written",
			"manifest_entries", len(encCtx.manifest),
			"data_size", len(tweetsJSON))

		// Emit encryption completed event
		s.emitEvent(domain.EventSeveritySuccess, domain.EventCategoryEncryption,
			"Streaming encryption completed",
			domain.EventMetadata{"export_id": exportID, "files_encrypted": len(encCtx.manifest) + 2})

		// Write encrypted index.html
		if err := s.writeEncryptedIndexHTML(opts.DestPath); err != nil {
			s.logger.Warn("failed to write encrypted index.html", "error", err)
		}

		// Write encrypted README
		if err := s.writeEncryptedReadme(opts.DestPath, len(exportedTweets), s.activeExport.BytesWritten); err != nil {
			s.logger.Warn("failed to write encrypted README", "error", err)
		}
	} else {
		// Non-encrypted: write tweets-data.json normally
		tweetsDataPath := filepath.Join(opts.DestPath, "tweets-data.json")
		s.logger.Info("writing tweets-data.json", "path", tweetsDataPath, "size", len(tweetsJSON))
		if err := writeFileSync(tweetsDataPath, tweetsJSON, 0644); err != nil {
			s.setExportError(fmt.Sprintf("write tweets-data.json: %v", err))
			return
		}

		// Copy offline-capable index.html
		s.mu.Lock()
		s.activeExport.CurrentFile = "Copying UI..."
		s.mu.Unlock()

		if err := s.copyOfflineUI(opts.DestPath); err != nil {
			s.setExportError(fmt.Sprintf("copy offline UI: %v", err))
			return
		}

		// Write regular README.txt
		if err := s.writeReadme(opts.DestPath, len(exportedTweets), s.activeExport.BytesWritten); err != nil {
			s.logger.Warn("failed to write README", "error", err)
		}
	}

	// Write export-metadata.json (separate file for easy access - not encrypted)
	exportMetadata := map[string]interface{}{
		"export_id":     s.activeExport.ID,
		"exported_at":   exportedAt.Format(time.RFC3339),
		"tweet_count":   len(exportedTweets),
		"media_count":   mediaCount,
		"bytes_written": s.activeExport.BytesWritten,
		"encrypted":     opts.Encrypt,
		"encryption": map[string]interface{}{
			"enabled":   opts.Encrypt,
			"algorithm": "AES-256-GCM",
			"kdf":       "Argon2id",
		},
		"format_version": "2.0",
		"app_name":       "xgrabba",
	}
	metadataJSON, _ := json.MarshalIndent(exportMetadata, "", "  ")
	if err := writeFileSync(filepath.Join(opts.DestPath, "export-metadata.json"), metadataJSON, 0644); err != nil {
		s.logger.Warn("failed to write export-metadata.json", "error", err)
	}

	// Copy viewer binaries if requested (works for both encrypted and non-encrypted)
	if opts.IncludeViewers && opts.ViewerBinDir != "" {
		s.mu.Lock()
		s.activeExport.CurrentFile = "Copying viewer binaries..."
		s.mu.Unlock()

		if err := s.copyViewerBinaries(opts.ViewerBinDir, opts.DestPath); err != nil {
			s.logger.Warn("failed to copy viewer binaries", "error", err)
		}
	}

	// Mark as completed
	s.mu.Lock()
	s.activeExport.Phase = "completed"
	s.activeExport.CurrentFile = ""
	exportID := s.activeExport.ID
	s.mu.Unlock()

	// Persist completed state
	s.saveExportState()

	s.mu.Lock()
	bytesWritten := s.activeExport.BytesWritten
	s.mu.Unlock()

	// Emit success event
	s.emitEvent(domain.EventSeveritySuccess, domain.EventCategoryExport,
		fmt.Sprintf("Export completed: %d tweets (%s)", len(exportedTweets), formatBytes(bytesWritten)),
		domain.EventMetadata{
			"export_id":     exportID,
			"tweet_count":   len(exportedTweets),
			"bytes_written": bytesWritten,
			"encrypted":     opts.Encrypt,
			"dest_path":     opts.DestPath,
		})

	s.logger.Info("async export complete",
		"tweets", len(exportedTweets),
		"bytes", bytesWritten,
		"encrypted", opts.Encrypt,
	)
}

// setExportError sets the export error state.
func (s *ExportService) setExportError(errMsg string) {
	s.mu.Lock()
	if s.activeExport != nil {
		s.activeExport.Phase = "failed"
		s.activeExport.Error = errMsg
	}
	exportID := ""
	destPath := ""
	if s.activeExport != nil {
		exportID = s.activeExport.ID
		destPath = s.activeExport.DestPath
	}
	s.mu.Unlock()

	// Emit error event
	s.emitEvent(domain.EventSeverityError, domain.EventCategoryExport,
		fmt.Sprintf("Export failed: %s", errMsg),
		domain.EventMetadata{"export_id": exportID, "dest_path": destPath, "error": errMsg})
}

// cleanupExport removes all xgrabba export files from the destination.
// Called when an export is cancelled to ensure no partial data is left behind.
func (s *ExportService) cleanupExport(destPath string) {
	// Remove xgrabba export files/directories
	filesToRemove := []string{
		"data",
		"encrypted",
		"index.html",
		"README.txt",
		"tweets-data.json",
		"export-metadata.json",
		"data.enc",
		"manifest.enc",
		"xgrabba-viewer.exe",
		"xgrabba-viewer-mac",
		"xgrabba-viewer-mac-arm64",
		"xgrabba-viewer-mac-amd64",
		"xgrabba-viewer-linux",
	}

	for _, name := range filesToRemove {
		path := filepath.Join(destPath, name)
		if info, err := os.Stat(path); err == nil {
			if info.IsDir() {
				if err := os.RemoveAll(path); err != nil {
					s.logger.Warn("failed to remove directory during cleanup", "path", path, "error", err)
				}
			} else {
				if err := os.Remove(path); err != nil {
					s.logger.Warn("failed to remove file during cleanup", "path", path, "error", err)
				}
			}
		}
	}

	// Sync filesystem to ensure cleanup is persisted
	if f, err := os.Open(destPath); err == nil {
		_ = f.Sync()
		f.Close()
	}

	s.logger.Info("export cleanup completed", "path", destPath)
}

// encryptExport encrypts all export files in place using parallel processing.
// It encrypts tweets-data.json → data.enc, and all files in data/ directory.
// Creates manifest.enc with mapping of original to encrypted file names.
// Uses AES-256-GCM with Argon2id key derivation for maximum security.
func (s *ExportService) encryptExport(destPath, password string) error {
	s.logger.Info("encrypting export", "path", destPath)

	// Create encrypted output directory
	encDir := filepath.Join(destPath, "encrypted")
	if err := os.MkdirAll(encDir, 0755); err != nil {
		return fmt.Errorf("create encrypted directory: %w", err)
	}

	// Derive encryption key ONCE (this is the slow part - ~1 second)
	s.mu.Lock()
	if s.activeExport != nil {
		s.activeExport.CurrentFile = "Deriving encryption key (AES-256 + Argon2id)..."
	}
	s.mu.Unlock()

	// Use parallel encryptor with progress callback
	numWorkers := runtime.NumCPU()
	if numWorkers < 2 {
		numWorkers = 2
	}
	if numWorkers > 8 {
		numWorkers = 8 // Cap to avoid memory issues
	}

	progressFn := func(completed, total int, currentFile string) {
		s.mu.Lock()
		if s.activeExport != nil {
			pct := (completed * 100) / total
			s.activeExport.CurrentFile = fmt.Sprintf("Encrypting: %s (%d/%d - %d%%)", filepath.Base(currentFile), completed, total, pct)
		}
		s.mu.Unlock()
	}

	parallelEnc, err := crypto.NewParallelEncryptor(password, numWorkers, progressFn)
	if err != nil {
		return fmt.Errorf("create encryptor: %w", err)
	}

	s.logger.Info("encryption key derived", "workers", numWorkers)

	// 1. Encrypt tweets-data.json → data.enc
	tweetsDataPath := filepath.Join(destPath, "tweets-data.json")
	if _, err := os.Stat(tweetsDataPath); err == nil {
		s.mu.Lock()
		if s.activeExport != nil {
			s.activeExport.CurrentFile = "Encrypting tweets-data.json..."
		}
		s.mu.Unlock()

		data, err := os.ReadFile(tweetsDataPath)
		if err != nil {
			return fmt.Errorf("read tweets-data.json: %w", err)
		}

		encrypted, err := parallelEnc.Encryptor().Encrypt(data)
		if err != nil {
			return fmt.Errorf("encrypt tweets-data.json: %w", err)
		}

		encPath := filepath.Join(destPath, "data.enc")
		if err := writeFileSync(encPath, encrypted, 0644); err != nil {
			return fmt.Errorf("write data.enc: %w", err)
		}

		// Remove original
		os.Remove(tweetsDataPath)
		s.logger.Info("encrypted tweets-data.json", "size", len(data), "encrypted_size", len(encrypted))
	}

	// 2. Collect all files to encrypt from data/ directory
	dataDir := filepath.Join(destPath, "data")
	var jobs []crypto.EncryptionJob

	if _, err := os.Stat(dataDir); err == nil {
		s.mu.Lock()
		if s.activeExport != nil {
			s.activeExport.CurrentFile = "Scanning media files..."
		}
		s.mu.Unlock()

		if err := filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			// Generate obfuscated name based on hash of path
			hash := sha256.Sum256([]byte(path))
			encName := hex.EncodeToString(hash[:8]) + ".enc"

			// Get relative path from destPath for manifest
			relPath, _ := filepath.Rel(destPath, path)

			jobs = append(jobs, crypto.EncryptionJob{
				SourcePath: path,
				DestPath:   filepath.Join(encDir, encName),
				RelPath:    relPath,
				EncName:    encName,
			})
			return nil
		}); err != nil {
			return fmt.Errorf("scan media files: %w", err)
		}

		s.logger.Info("encrypting media files", "count", len(jobs), "workers", numWorkers)

		// 3. Encrypt all files in parallel
		manifest, errs := parallelEnc.EncryptFiles(jobs)

		for _, err := range errs {
			s.logger.Warn("encryption error", "error", err)
		}

		// Remove original data directory
		os.RemoveAll(dataDir)

		// 4. Write encrypted manifest
		s.mu.Lock()
		if s.activeExport != nil {
			s.activeExport.CurrentFile = "Finalizing encryption..."
		}
		s.mu.Unlock()

		manifestData, err := json.Marshal(manifest)
		if err != nil {
			return fmt.Errorf("marshal manifest: %w", err)
		}

		encManifest, err := parallelEnc.Encryptor().Encrypt(manifestData)
		if err != nil {
			return fmt.Errorf("encrypt manifest: %w", err)
		}

		manifestPath := filepath.Join(destPath, "manifest.enc")
		if err := writeFileSync(manifestPath, encManifest, 0644); err != nil {
			return fmt.Errorf("write manifest.enc: %w", err)
		}

		s.logger.Info("encryption complete", "files_encrypted", len(manifest)+1)
	} else {
		// No data directory, just write empty manifest
		manifestData, _ := json.Marshal(map[string]string{})
		encManifest, err := parallelEnc.Encryptor().Encrypt(manifestData)
		if err != nil {
			return fmt.Errorf("encrypt manifest: %w", err)
		}
		manifestPath := filepath.Join(destPath, "manifest.enc")
		if err := writeFileSync(manifestPath, encManifest, 0644); err != nil {
			return fmt.Errorf("write manifest.enc: %w", err)
		}
	}

	// 5. Replace index.html with encrypted archive notice
	if err := s.writeEncryptedIndexHTML(destPath); err != nil {
		s.logger.Warn("failed to write encrypted index.html", "error", err)
	}

	return nil
}

// writeEncryptedIndexHTML writes an index.html that explains the archive is encrypted.
func (s *ExportService) writeEncryptedIndexHTML(destPath string) error {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Encrypted XGrabba Archive</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background: #15202b;
            color: #e7e9ea;
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
            padding: 20px;
        }
        .container {
            max-width: 500px;
            text-align: center;
        }
        .lock-icon {
            width: 80px;
            height: 80px;
            margin: 0 auto 24px;
            color: #1d9bf0;
        }
        h1 { font-size: 24px; margin-bottom: 16px; }
        p { color: #8b98a5; margin-bottom: 24px; line-height: 1.6; }
        .instructions {
            background: #1e2732;
            border-radius: 12px;
            padding: 20px;
            text-align: left;
            margin-bottom: 24px;
        }
        .instructions h2 {
            font-size: 14px;
            color: #8b98a5;
            margin-bottom: 12px;
            text-transform: uppercase;
            letter-spacing: 0.5px;
        }
        .instructions ol {
            padding-left: 20px;
        }
        .instructions li {
            margin-bottom: 8px;
            font-size: 14px;
        }
        .viewer-list {
            display: grid;
            gap: 8px;
        }
        .viewer-item {
            background: #1e2732;
            padding: 12px 16px;
            border-radius: 8px;
            font-size: 13px;
            display: flex;
            justify-content: space-between;
            align-items: center;
        }
        .viewer-item .os { color: #8b98a5; }
    </style>
</head>
<body>
    <div class="container">
        <svg class="lock-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="11" width="18" height="11" rx="2" ry="2"></rect>
            <path d="M7 11V7a5 5 0 0 1 10 0v4"></path>
        </svg>
        <h1>Encrypted Archive</h1>
        <p>This XGrabba archive is protected with AES-256-GCM encryption. To view your tweets, use one of the viewer applications below.</p>

        <div class="instructions">
            <h2>How to View</h2>
            <ol>
                <li>Run the viewer app for your operating system</li>
                <li>Enter your encryption password when prompted</li>
                <li>Browse your archive in your web browser</li>
            </ol>
        </div>

        <div class="viewer-list">
            <div class="viewer-item">
                <span>xgrabba-viewer.exe</span>
                <span class="os">Windows</span>
            </div>
            <div class="viewer-item">
                <span>xgrabba-viewer-mac-arm64</span>
                <span class="os">macOS (Apple Silicon)</span>
            </div>
            <div class="viewer-item">
                <span>xgrabba-viewer-mac-amd64</span>
                <span class="os">macOS (Intel)</span>
            </div>
            <div class="viewer-item">
                <span>xgrabba-viewer-linux</span>
                <span class="os">Linux</span>
            </div>
        </div>
    </div>
</body>
</html>`

	return writeFileSync(filepath.Join(destPath, "index.html"), []byte(html), 0644)
}

// writeEncryptedReadme writes a README for encrypted archives.
func (s *ExportService) writeEncryptedReadme(destPath string, tweetCount int, totalBytes int64) error {
	sizeStr := formatBytes(totalBytes)
	now := time.Now().UTC()
	dateStr := now.Format("January 2, 2006 at 3:04:05 PM UTC")

	readme := fmt.Sprintf(`================================================================================
                    ENCRYPTED XGRABBA ARCHIVE
================================================================================

ARCHIVE INFORMATION
-------------------
Tweets Archived:  %d
Total Data Size:  %s
Export Date:      %s
Encryption:       AES-256-GCM with Argon2id key derivation

================================================================================

THIS ARCHIVE IS ENCRYPTED

Your tweets and media are protected with strong encryption. The archive cannot
be read without your password.

To view your archive, run one of the viewer applications included in this folder.

================================================================================

VIEWER APPLICATIONS
-------------------

Windows:
  Double-click xgrabba-viewer.exe
  If SmartScreen appears: Click "More info" → "Run anyway"

macOS (Apple Silicon M1/M2/M3/M4):
  Right-click xgrabba-viewer-mac-arm64 → Open
  If blocked: System Settings → Privacy & Security → Open Anyway

macOS (Intel):
  Right-click xgrabba-viewer-mac-amd64 → Open

Linux:
  chmod +x xgrabba-viewer-linux
  ./xgrabba-viewer-linux

================================================================================

SECURITY NOTES
--------------

• Your password is never stored - if you forget it, the data cannot be recovered
• Each file is encrypted with a unique key derived from your password
• The encryption uses AES-256-GCM (authenticated encryption)
• Key derivation uses Argon2id (memory-hard, resistant to GPU attacks)

================================================================================

FILE STRUCTURE
--------------

README.txt        - This file
index.html        - Explains the archive is encrypted
data.enc          - Encrypted tweet index
manifest.enc      - Encrypted file mapping
encrypted/        - Encrypted media files
xgrabba-viewer.*  - Viewer applications for each platform

================================================================================

DISCLAIMER
----------

XGrabba is an open source project for personal archival purposes. The creators
are not responsible for the data you choose to archive. Use responsibly.

Source: https://github.com/iconidentify/xgrabba

================================================================================
`, tweetCount, sizeStr, dateStr)

	return writeFileSync(filepath.Join(destPath, "README.txt"), []byte(readme), 0644)
}

// writeFileSync writes data to a file and ensures it's flushed to disk.
// This is important for USB drives (especially exFAT) which may not flush immediately.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	if err != nil {
		f.Close()
		return err
	}

	// Sync to ensure data is written to disk
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}

	return f.Close()
}

// GetExportStatus returns the current export status.
func (s *ExportService) GetExportStatus() *ActiveExport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeExport == nil {
		return &ActiveExport{Phase: "idle"}
	}
	// Return a copy to avoid race conditions
	return &ActiveExport{
		ID:             s.activeExport.ID,
		DestPath:       s.activeExport.DestPath,
		MountPoint:     s.activeExport.MountPoint,
		Phase:          s.activeExport.Phase,
		TotalTweets:    s.activeExport.TotalTweets,
		ExportedTweets: s.activeExport.ExportedTweets,
		BytesWritten:   s.activeExport.BytesWritten,
		CurrentFile:    s.activeExport.CurrentFile,
		StartedAt:      s.activeExport.StartedAt,
		Error:          s.activeExport.Error,
		ZipPath:        s.activeExport.ZipPath,
	}
}

// CancelExport cancels an in-progress export.
func (s *ExportService) CancelExport() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeExport == nil {
		return fmt.Errorf("no export in progress")
	}

	if s.activeExport.Phase != "preparing" && s.activeExport.Phase != "exporting" && s.activeExport.Phase != "finalizing" {
		return fmt.Errorf("export not in progress (phase: %s)", s.activeExport.Phase)
	}

	// Immediately set phase to cancelled so UI updates right away
	s.activeExport.Phase = "cancelled"
	s.activeExport.CurrentFile = "Cancelling export..."
	s.activeExport.Error = "Export cancelled by user"
	s.mu.Unlock()

	// Persist cancelled state
	s.saveExportState()

	// Cancel the context to stop the export goroutine
	s.mu.Lock()
	if s.activeExport.cancelFunc != nil {
		s.activeExport.cancelFunc()
	}
	s.mu.Unlock()

	return nil
}

// IsExportUsingPath checks if an active export is writing to a path under the given mount point.
func (s *ExportService) IsExportUsingPath(mountPoint string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeExport == nil {
		return false
	}

	// Check if export is in an active phase
	phase := s.activeExport.Phase
	if phase != "preparing" && phase != "exporting" && phase != "finalizing" && phase != "encrypting" {
		return false
	}

	// Check if mount point matches
	if s.activeExport.MountPoint != "" && s.activeExport.MountPoint == mountPoint {
		return true
	}

	// Also check if DestPath starts with the mount point
	if strings.HasPrefix(s.activeExport.DestPath, mountPoint) {
		return true
	}

	return false
}

// CancelExportForPath cancels any export that is using the given mount point.
// Returns true if an export was cancelled.
func (s *ExportService) CancelExportForPath(mountPoint string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeExport == nil {
		return false
	}

	// Check if export is in an active phase
	phase := s.activeExport.Phase
	if phase != "preparing" && phase != "exporting" && phase != "finalizing" && phase != "encrypting" {
		return false
	}

	// Check if this export is using the mount point
	isUsing := false
	if s.activeExport.MountPoint != "" && s.activeExport.MountPoint == mountPoint {
		isUsing = true
	} else if strings.HasPrefix(s.activeExport.DestPath, mountPoint) {
		isUsing = true
	}

	if !isUsing {
		return false
	}

	// Cancel the export
	if s.activeExport.cancelFunc != nil {
		s.activeExport.cancelFunc()
	}
	s.activeExport.Phase = "cancelled"
	s.activeExport.Error = "Cancelled due to USB drive unmount"

	return true
}

// StartDownloadExportAsync starts an export that creates a downloadable zip file.
func (s *ExportService) StartDownloadExportAsync(opts ExportOptions) (string, error) {
	s.mu.Lock()
	if s.activeExport != nil && (s.activeExport.Phase == "preparing" || s.activeExport.Phase == "exporting" || s.activeExport.Phase == "finalizing") {
		s.mu.Unlock()
		return "", ErrExportInProgress
	}

	// Generate export ID
	exportID := fmt.Sprintf("exp_%d", time.Now().UnixNano())

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	s.activeExport = &ActiveExport{
		ID:         exportID,
		DestPath:   "download",
		Phase:      "preparing",
		StartedAt:  time.Now(),
		cancelFunc: cancel,
	}
	s.mu.Unlock()

	// Start export in background
	go s.runDownloadExportAsync(ctx, opts)

	return exportID, nil
}

// runDownloadExportAsync creates a zip file with the export and tracks progress.
func (s *ExportService) runDownloadExportAsync(ctx context.Context, opts ExportOptions) {
	defer func() {
		// Ensure phase is set on exit if not already completed/failed/cancelled
		s.mu.Lock()
		if s.activeExport != nil && s.activeExport.Phase != "completed" && s.activeExport.Phase != "failed" && s.activeExport.Phase != "cancelled" {
			s.activeExport.Phase = "failed"
			s.activeExport.Error = "unexpected exit"
		}
		s.mu.Unlock()
	}()

	// Create temp directory for export
	tempDir, err := os.MkdirTemp("", "xgrabba-export-*")
	if err != nil {
		s.setExportError(fmt.Sprintf("create temp directory: %v", err))
		return
	}

	// Get all tweets
	tweets, _, err := s.tweetSvc.List(ctx, 0, 0)
	if err != nil {
		s.setExportError(fmt.Sprintf("list tweets: %v", err))
		return
	}

	// Apply filters
	if opts.DateRange != nil || len(opts.Authors) > 0 || opts.SearchQuery != "" {
		tweets = s.filterTweets(tweets, opts)
	}

	// Sort by date (newest first)
	sort.Slice(tweets, func(i, j int) bool {
		return tweets[i].CreatedAt.After(tweets[j].CreatedAt)
	})

	// Update total count
	s.mu.Lock()
	s.activeExport.TotalTweets = len(tweets)
	s.activeExport.Phase = "exporting"
	s.mu.Unlock()

	// Create data directory
	dataDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		s.setExportError(fmt.Sprintf("create data directory: %v", err))
		return
	}

	// Export tweets and media
	exportedTweets := make([]ExportedTweet, 0, len(tweets))

	for i, tweet := range tweets {
		select {
		case <-ctx.Done():
			os.RemoveAll(tempDir)
			s.mu.Lock()
			s.activeExport.Phase = "cancelled"
			s.mu.Unlock()
			return
		default:
		}

		// Update progress
		s.mu.Lock()
		s.activeExport.ExportedTweets = i
		s.activeExport.CurrentFile = fmt.Sprintf("%s (@%s)", tweet.AITitle, tweet.Author.Username)
		s.mu.Unlock()

		exported, size, _, err := s.exportTweet(ctx, tweet, dataDir, nil)
		if err != nil {
			s.logger.Warn("failed to export tweet", "tweet_id", tweet.ID, "error", err)
			continue
		}

		exportedTweets = append(exportedTweets, *exported)

		s.mu.Lock()
		s.activeExport.BytesWritten += size
		s.mu.Unlock()
	}

	// Update phase to finalizing
	s.mu.Lock()
	s.activeExport.Phase = "finalizing"
	s.activeExport.ExportedTweets = len(exportedTweets)
	s.activeExport.CurrentFile = "Writing metadata..."
	s.mu.Unlock()

	// Write tweets-data.json
	tweetsDataPath := filepath.Join(tempDir, "tweets-data.json")
	tweetsData := map[string]interface{}{
		"tweets":      exportedTweets,
		"total":       len(exportedTweets),
		"exported_at": time.Now().UTC(),
		"version":     "1.0",
	}

	tweetsJSON, err := json.MarshalIndent(tweetsData, "", "  ")
	if err != nil {
		os.RemoveAll(tempDir)
		s.setExportError(fmt.Sprintf("marshal tweets data: %v", err))
		return
	}

	if err := os.WriteFile(tweetsDataPath, tweetsJSON, 0644); err != nil {
		os.RemoveAll(tempDir)
		s.setExportError(fmt.Sprintf("write tweets-data.json: %v", err))
		return
	}

	s.mu.Lock()
	s.activeExport.CurrentFile = "Copying UI..."
	s.mu.Unlock()

	// Copy offline-capable index.html
	if err := s.copyOfflineUI(tempDir); err != nil {
		os.RemoveAll(tempDir)
		s.setExportError(fmt.Sprintf("copy offline UI: %v", err))
		return
	}

	// Write README.txt
	if err := s.writeReadme(tempDir, len(exportedTweets), s.activeExport.BytesWritten); err != nil {
		s.logger.Warn("failed to write README", "error", err)
	}

	s.mu.Lock()
	s.activeExport.CurrentFile = "Creating zip archive..."
	s.mu.Unlock()

	// Create zip file
	zipPath := filepath.Join(os.TempDir(), fmt.Sprintf("xgrabba-archive-%s.zip", time.Now().Format("2006-01-02")))
	if err := s.createZipFromDir(tempDir, zipPath); err != nil {
		os.RemoveAll(tempDir)
		s.setExportError(fmt.Sprintf("create zip: %v", err))
		return
	}

	// Clean up temp directory
	os.RemoveAll(tempDir)

	// Mark as completed with zip path
	s.mu.Lock()
	s.activeExport.Phase = "completed"
	s.activeExport.CurrentFile = ""
	s.activeExport.ZipPath = zipPath
	s.mu.Unlock()

	s.logger.Info("download export complete",
		"tweets", len(exportedTweets),
		"bytes", s.activeExport.BytesWritten,
		"zip_path", zipPath,
	)
}

// createZipFromDir creates a zip archive from a directory.
func (s *ExportService) createZipFromDir(srcDir, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip file: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Walk the source directory
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if path == srcDir {
			return nil
		}

		// Get relative path for zip entry
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Create proper zip path (forward slashes)
		zipEntryPath := filepath.ToSlash(relPath)

		if info.IsDir() {
			// Create directory entry
			_, err := zipWriter.Create(zipEntryPath + "/")
			return err
		}

		// Create file entry
		writer, err := zipWriter.Create(zipEntryPath)
		if err != nil {
			return err
		}

		// Copy file contents
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})
}

// GetDownloadZipPath returns the path to the completed zip file.
func (s *ExportService) GetDownloadZipPath() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeExport == nil {
		return "", fmt.Errorf("no export available")
	}

	if s.activeExport.Phase != "completed" {
		return "", fmt.Errorf("export not completed (phase: %s)", s.activeExport.Phase)
	}

	if s.activeExport.ZipPath == "" {
		return "", fmt.Errorf("no download available for this export")
	}

	return s.activeExport.ZipPath, nil
}

// CleanupDownloadExport removes the zip file after download.
func (s *ExportService) CleanupDownloadExport() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeExport != nil && s.activeExport.ZipPath != "" {
		os.Remove(s.activeExport.ZipPath)
		s.activeExport.ZipPath = ""
	}
}

// ExportOptions configures the export process.
type ExportOptions struct {
	DestPath       string     // Destination directory (e.g., USB drive path)
	MountPoint     string     // USB mount point (for tracking which drive is in use)
	IncludeViewers bool       // Include cross-platform viewer binaries
	ViewerBinDir   string     // Directory containing viewer binaries
	DateRange      *DateRange // Optional date filter
	Authors        []string   // Optional author filter
	SearchQuery    string     // Optional search filter
	Encrypt        bool       // Enable AES-256-GCM encryption
	Password       string     // Password for encryption (required if Encrypt is true)
}

// DateRange filters tweets by date.
type DateRange struct {
	Start time.Time
	End   time.Time
}

// ExportProgress tracks export progress.
type ExportProgress struct {
	Phase          string `json:"phase"`
	TotalTweets    int    `json:"total_tweets"`
	ExportedTweets int    `json:"exported_tweets"`
	TotalFiles     int    `json:"total_files"`
	CopiedFiles    int    `json:"copied_files"`
	Error          string `json:"error,omitempty"`
}

// ExportResult contains the result of an export operation.
type ExportResult struct {
	DestPath    string    `json:"dest_path"`
	TweetsCount int       `json:"tweets_count"`
	MediaCount  int       `json:"media_count"`
	TotalSize   int64     `json:"total_size_bytes"`
	ExportedAt  time.Time `json:"exported_at"`
}

// ExportedTweet is the structure used in tweets-data.json for offline viewing.
type ExportedTweet struct {
	TweetID       string              `json:"tweet_id"`
	URL           string              `json:"url"`
	Author        ExportedAuthor      `json:"author"`
	Text          string              `json:"text"`
	PostedAt      time.Time           `json:"posted_at"`
	ArchivedAt    time.Time           `json:"archived_at"`
	Media         []ExportedMedia     `json:"media"`
	Metrics       domain.TweetMetrics `json:"metrics"`
	AITitle       string              `json:"ai_title"`
	AISummary     string              `json:"ai_summary,omitempty"`
	AITags        []string            `json:"ai_tags,omitempty"`
	AIContentType string              `json:"ai_content_type,omitempty"`
	AITopics      []string            `json:"ai_topics,omitempty"`
	ArchivePath   string              `json:"archive_path"` // Relative path for media lookup
}

// ExportedAuthor contains author info for offline viewing.
type ExportedAuthor struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarPath  string `json:"avatar_path,omitempty"` // Relative path to avatar
	Verified    bool   `json:"verified,omitempty"`
}

// ExportedMedia contains media info for offline viewing.
type ExportedMedia struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	LocalPath          string   `json:"local_path"` // Relative path from archive root
	ThumbnailPath      string   `json:"thumbnail_path,omitempty"`
	Width              int      `json:"width,omitempty"`
	Height             int      `json:"height,omitempty"`
	Duration           int      `json:"duration_seconds,omitempty"`
	AICaption          string   `json:"ai_caption,omitempty"`
	AITags             []string `json:"ai_tags,omitempty"`
	Transcript         string   `json:"transcript,omitempty"`
	TranscriptLanguage string   `json:"transcript_language,omitempty"`
}

// ExportToUSB exports the archive to a USB drive or directory.
func (s *ExportService) ExportToUSB(ctx context.Context, opts ExportOptions) (*ExportResult, error) {
	// Sanitize path: remove shell-style backslash escapes (e.g., "\ " -> " ")
	opts.DestPath = strings.ReplaceAll(opts.DestPath, "\\ ", " ")
	opts.DestPath = strings.ReplaceAll(opts.DestPath, "\\(", "(")
	opts.DestPath = strings.ReplaceAll(opts.DestPath, "\\)", ")")
	opts.DestPath = strings.ReplaceAll(opts.DestPath, "\\'", "'")

	// Expand ~ to home directory
	if strings.HasPrefix(opts.DestPath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			opts.DestPath = filepath.Join(home, opts.DestPath[2:])
		}
	}

	// Clean path: remove trailing slashes and normalize
	opts.DestPath = filepath.Clean(opts.DestPath)

	s.logger.Info("starting export",
		"dest", opts.DestPath,
		"include_viewers", opts.IncludeViewers,
	)

	// Validate destination
	if opts.DestPath == "" {
		return nil, fmt.Errorf("destination path is required")
	}

	// Check if destination exists - if so, test write access directly on it
	// If not, check parent directory
	var writeTestDir string
	if info, err := os.Stat(opts.DestPath); err == nil && info.IsDir() {
		// Destination exists and is a directory (e.g., USB mount point)
		writeTestDir = opts.DestPath
	} else {
		// Destination doesn't exist, check parent
		parentDir := filepath.Dir(opts.DestPath)
		if info, err := os.Stat(parentDir); err != nil {
			return nil, fmt.Errorf("parent directory does not exist: %s", parentDir)
		} else if !info.IsDir() {
			return nil, fmt.Errorf("parent path is not a directory: %s", parentDir)
		}
		writeTestDir = parentDir
	}

	// Test write access
	testFile := filepath.Join(writeTestDir, ".xgrabba_write_test")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		return nil, fmt.Errorf("no write permission on %s: %v", writeTestDir, err)
	}
	os.Remove(testFile)

	// Create destination directory (no-op if already exists)
	if err := os.MkdirAll(opts.DestPath, 0755); err != nil {
		return nil, fmt.Errorf("create destination directory: %w", err)
	}

	// Get all tweets
	tweets, total, err := s.tweetSvc.List(ctx, 0, 0) // 0 limit = all
	if err != nil {
		return nil, fmt.Errorf("list tweets: %w", err)
	}
	s.logger.Info("found tweets to export", "count", total)

	// Filter tweets if filters are specified
	if opts.DateRange != nil || len(opts.Authors) > 0 || opts.SearchQuery != "" {
		tweets = s.filterTweets(tweets, opts)
		s.logger.Info("filtered tweets", "count", len(tweets))
	}

	// Sort by date (newest first)
	sort.Slice(tweets, func(i, j int) bool {
		return tweets[i].CreatedAt.After(tweets[j].CreatedAt)
	})

	// Create data directory
	dataDir := filepath.Join(opts.DestPath, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	// Export tweets and media
	exportedTweets := make([]ExportedTweet, 0, len(tweets))
	var totalSize int64
	var mediaCount int

	for i, tweet := range tweets {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if i%50 == 0 {
			s.logger.Info("export progress", "exported", i, "total", len(tweets))
		}

		exported, size, count, err := s.exportTweet(ctx, tweet, dataDir, nil)
		if err != nil {
			s.logger.Warn("failed to export tweet", "tweet_id", tweet.ID, "error", err)
			continue
		}

		exportedTweets = append(exportedTweets, *exported)
		totalSize += size
		mediaCount += count
	}

	// Write tweets-data.json
	tweetsDataPath := filepath.Join(opts.DestPath, "tweets-data.json")
	tweetsData := map[string]interface{}{
		"tweets":      exportedTweets,
		"total":       len(exportedTweets),
		"exported_at": time.Now().UTC(),
		"version":     "1.0",
	}

	tweetsJSON, err := json.MarshalIndent(tweetsData, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal tweets data: %w", err)
	}

	if err := os.WriteFile(tweetsDataPath, tweetsJSON, 0644); err != nil {
		return nil, fmt.Errorf("write tweets-data.json: %w", err)
	}

	// Copy offline-capable index.html
	if err := s.copyOfflineUI(opts.DestPath); err != nil {
		return nil, fmt.Errorf("copy offline UI: %w", err)
	}

	// Copy viewer binaries if requested
	if opts.IncludeViewers && opts.ViewerBinDir != "" {
		if err := s.copyViewerBinaries(opts.ViewerBinDir, opts.DestPath); err != nil {
			s.logger.Warn("failed to copy viewer binaries", "error", err)
			// Don't fail the export, just log warning
		}
	}

	// Write README.txt
	if err := s.writeReadme(opts.DestPath, len(exportedTweets), s.activeExport.BytesWritten); err != nil {
		s.logger.Warn("failed to write README", "error", err)
	}

	result := &ExportResult{
		DestPath:    opts.DestPath,
		TweetsCount: len(exportedTweets),
		MediaCount:  mediaCount,
		TotalSize:   totalSize,
		ExportedAt:  time.Now(),
	}

	s.logger.Info("export complete",
		"tweets", result.TweetsCount,
		"media", result.MediaCount,
		"size_mb", result.TotalSize/(1024*1024),
	)

	return result, nil
}

// filterTweets applies optional filters to the tweet list.
func (s *ExportService) filterTweets(tweets []*domain.Tweet, opts ExportOptions) []*domain.Tweet {
	filtered := make([]*domain.Tweet, 0)

	for _, tweet := range tweets {
		// Date filter
		if opts.DateRange != nil {
			if tweet.PostedAt.Before(opts.DateRange.Start) || tweet.PostedAt.After(opts.DateRange.End) {
				continue
			}
		}

		// Author filter
		if len(opts.Authors) > 0 {
			found := false
			for _, author := range opts.Authors {
				if strings.EqualFold(tweet.Author.Username, author) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Search filter
		if opts.SearchQuery != "" && !s.tweetSvc.tweetMatchesQuery(tweet, strings.ToLower(opts.SearchQuery)) {
			continue
		}

		filtered = append(filtered, tweet)
	}

	return filtered
}

// exportTweet exports a single tweet and its media, returning the exported data and stats.
// If encCtx is provided, files are encrypted as they're copied using streaming encryption.
func (s *ExportService) exportTweet(ctx context.Context, tweet *domain.Tweet, dataDir string, encCtx *encryptionContext) (*ExportedTweet, int64, int, error) {
	// Build relative archive path (YYYY/MM/username_date_tweetID)
	year := tweet.PostedAt.Format("2006")
	month := tweet.PostedAt.Format("01")
	folderName := fmt.Sprintf("%s_%s_%s",
		tweet.Author.Username,
		tweet.PostedAt.Format("2006-01-02"),
		tweet.ID,
	)
	relArchivePath := filepath.Join(year, month, folderName)
	destArchivePath := filepath.Join(dataDir, relArchivePath)

	// Create archive directory only if not encrypting
	// (encrypted files go directly to encrypted/ directory)
	if encCtx == nil {
		if err := os.MkdirAll(filepath.Join(destArchivePath, "media"), 0755); err != nil {
			return nil, 0, 0, fmt.Errorf("create archive directory: %w", err)
		}
	}

	var totalSize int64
	var mediaCount int

	// Copy media files in parallel for better performance
	exportedMedia := make([]ExportedMedia, 0, len(tweet.Media))
	if len(tweet.Media) > 0 {
		// Use worker pool for parallel processing
		numWorkers := runtime.NumCPU()
		if numWorkers > 4 {
			numWorkers = 4 // Cap to avoid overwhelming USB
		}
		if numWorkers < 1 {
			numWorkers = 1
		}

		type mediaResult struct {
			exported *ExportedMedia
			size     int64
			err      error
		}

		mediaChan := make(chan *domain.Media, len(tweet.Media))
		resultChan := make(chan mediaResult, len(tweet.Media))

		// Send all media to channel
		for i := range tweet.Media {
			mediaChan <- &tweet.Media[i]
		}
		close(mediaChan)

		// Start workers
		var wg sync.WaitGroup
		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for media := range mediaChan {
					exported, size, err := s.exportMedia(ctx, media, tweet.ArchivePath, destArchivePath, relArchivePath, encCtx)
					resultChan <- mediaResult{exported: exported, size: size, err: err}
				}
			}()
		}

		// Close result channel when all workers done
		go func() {
			wg.Wait()
			close(resultChan)
		}()

		// Collect results
		for result := range resultChan {
			if result.err != nil {
				s.logger.Warn("failed to export media", "error", result.err)
				continue
			}
			if result.exported != nil {
				exportedMedia = append(exportedMedia, *result.exported)
				totalSize += result.size
				mediaCount++
			}
		}
	}

	// Copy avatar if exists
	var avatarPath string
	srcAvatarPath := filepath.Join(tweet.ArchivePath, "avatar.jpg")
	if _, err := os.Stat(srcAvatarPath); err == nil {
		relAvatarPath := filepath.Join("data", relArchivePath, "avatar.jpg")

		if encCtx != nil {
			// Streaming encryption for avatar
			if size, err := encCtx.encryptingCopyFile(ctx, srcAvatarPath, relAvatarPath); err == nil {
				avatarPath = relAvatarPath
				totalSize += size
			}
		} else {
			// No encryption: copy normally
			destAvatarPath := filepath.Join(destArchivePath, "avatar.jpg")
			if size, err := copyFile(srcAvatarPath, destAvatarPath); err == nil {
				avatarPath = relAvatarPath
				totalSize += size
			}
		}
	}

	// Build exported tweet
	archivedAt := time.Now()
	if tweet.ArchivedAt != nil {
		archivedAt = *tweet.ArchivedAt
	}

	exported := &ExportedTweet{
		TweetID: string(tweet.ID),
		URL:     tweet.URL,
		Author: ExportedAuthor{
			ID:          tweet.Author.ID,
			Username:    tweet.Author.Username,
			DisplayName: tweet.Author.DisplayName,
			AvatarPath:  avatarPath,
			Verified:    tweet.Author.Verified,
		},
		Text:          tweet.Text,
		PostedAt:      tweet.PostedAt,
		ArchivedAt:    archivedAt,
		Media:         exportedMedia,
		Metrics:       tweet.Metrics,
		AITitle:       tweet.AITitle,
		AISummary:     tweet.AISummary,
		AITags:        tweet.AITags,
		AIContentType: tweet.AIContentType,
		AITopics:      tweet.AITopics,
		ArchivePath:   filepath.Join("data", relArchivePath),
	}

	return exported, totalSize, mediaCount, nil
}

// exportMedia exports a single media file.
// If encCtx is provided, files are encrypted as they're copied using streaming encryption.
func (s *ExportService) exportMedia(ctx context.Context, media *domain.Media, srcArchivePath, destArchivePath, relArchivePath string, encCtx *encryptionContext) (*ExportedMedia, int64, error) {
	var totalSize int64

	exported := &ExportedMedia{
		ID:                 media.ID,
		Type:               string(media.Type),
		Width:              media.Width,
		Height:             media.Height,
		Duration:           media.Duration,
		AICaption:          media.AICaption,
		AITags:             media.AITags,
		Transcript:         media.Transcript,
		TranscriptLanguage: media.TranscriptLanguage,
	}

	// Copy main media file
	if media.LocalPath != "" {
		filename := filepath.Base(media.LocalPath)
		srcPath := media.LocalPath
		relPath := filepath.Join("data", relArchivePath, "media", filename)

		if encCtx != nil {
			// Streaming encryption: encrypt as we copy, file goes directly to encrypted/
			if size, err := encCtx.encryptingCopyFile(ctx, srcPath, relPath); err == nil {
				exported.LocalPath = relPath
				totalSize += size
			} else {
				s.logger.Warn("failed to encrypt media file", "src", srcPath, "error", err)
			}
		} else {
			// No encryption: copy normally
			destPath := filepath.Join(destArchivePath, "media", filename)
			if size, err := copyFile(srcPath, destPath); err == nil {
				exported.LocalPath = relPath
				totalSize += size
			} else {
				s.logger.Warn("failed to copy media file", "src", srcPath, "error", err)
			}
		}
	}

	// Copy thumbnail for videos
	if media.Type == domain.MediaTypeVideo || media.Type == domain.MediaTypeGIF {
		thumbFilename := fmt.Sprintf("%s_thumb.jpg", media.ID)
		srcThumbPath := filepath.Join(srcArchivePath, "media", thumbFilename)

		if _, err := os.Stat(srcThumbPath); err == nil {
			relThumbPath := filepath.Join("data", relArchivePath, "media", thumbFilename)

			if encCtx != nil {
				// Streaming encryption for thumbnail
				if size, err := encCtx.encryptingCopyFile(ctx, srcThumbPath, relThumbPath); err == nil {
					exported.ThumbnailPath = relThumbPath
					totalSize += size
				}
			} else {
				// No encryption: copy normally
				destThumbPath := filepath.Join(destArchivePath, "media", thumbFilename)
				if size, err := copyFile(srcThumbPath, destThumbPath); err == nil {
					exported.ThumbnailPath = relThumbPath
					totalSize += size
				}
			}
		}
	}

	return exported, totalSize, nil
}

// copyFile copies a file and returns its size.
// It syncs the destination file to ensure data is flushed to disk (critical for USB drives)
// and verifies the destination file size matches the source.
func copyFile(src, dst string) (int64, error) {
	srcFile, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer srcFile.Close()

	srcStat, err := srcFile.Stat()
	if err != nil {
		return 0, err
	}
	srcSize := srcStat.Size()

	dstFile, err := os.Create(dst)
	if err != nil {
		return 0, err
	}

	written, err := io.Copy(dstFile, srcFile)
	if err != nil {
		dstFile.Close()
		return 0, err
	}

	// Sync to ensure data is written to disk - critical for USB drives with delayed writes
	if err := dstFile.Sync(); err != nil {
		dstFile.Close()
		return 0, fmt.Errorf("sync destination file: %w", err)
	}

	if err := dstFile.Close(); err != nil {
		return 0, fmt.Errorf("close destination file: %w", err)
	}

	// Verify written bytes match source size
	if written != srcSize {
		return 0, fmt.Errorf("size mismatch: wrote %d bytes, expected %d", written, srcSize)
	}

	// Double-check by statting the destination file
	dstStat, err := os.Stat(dst)
	if err != nil {
		return 0, fmt.Errorf("stat destination file: %w", err)
	}
	if dstStat.Size() != srcSize {
		return 0, fmt.Errorf("destination file size mismatch: got %d bytes, expected %d", dstStat.Size(), srcSize)
	}

	return srcSize, nil
}

// encryptionContext holds state for streaming encryption during export.
// This allows encrypting files as they're copied, so unencrypted data never touches the USB.
type encryptionContext struct {
	encryptor *crypto.Encryptor
	encDir    string                   // Directory for encrypted files
	manifest  map[string]manifestEntry // Maps original relative path to manifest entry
	mu        sync.Mutex               // Protects manifest
	skipSync  bool                     // Skip per-file syncs for better USB performance
	syncFiles []string                 // Files to sync at the end (if skipSync is true)
	syncMu    sync.Mutex               // Protects syncFiles
}

type manifestEntry struct {
	EncryptedName string `json:"enc_name"`
	OriginalSize  int64  `json:"original_size"`
	ChunkCount    int    `json:"chunk_count"`
	ContentType   string `json:"content_type"`
}

type manifestFile struct {
	Version   int                      `json:"version"`
	ChunkSize int                      `json:"chunk_size"`
	Entries   map[string]manifestEntry `json:"entries"`
}

// newEncryptionContext creates a new encryption context for streaming encryption.
// skipSync should be true for USB drives to improve performance (syncs batched at end).
func newEncryptionContext(password, destPath string, skipSync bool) (*encryptionContext, error) {
	enc, err := crypto.NewEncryptor(password)
	if err != nil {
		return nil, fmt.Errorf("create encryptor: %w", err)
	}

	encDir := filepath.Join(destPath, "encrypted")
	if err := os.MkdirAll(encDir, 0755); err != nil {
		return nil, fmt.Errorf("create encrypted directory: %w", err)
	}

	return &encryptionContext{
		encryptor: enc,
		encDir:    encDir,
		manifest:  make(map[string]manifestEntry),
		skipSync:  skipSync,
		syncFiles: make([]string, 0),
	}, nil
}

// encryptingCopyFile copies a file while encrypting it using streaming encryption.
// The encrypted file is written to the encrypted directory with an obfuscated name.
// Returns the number of source bytes read (not encrypted bytes written).
func (ec *encryptionContext) encryptingCopyFile(ctx context.Context, src, relPath string) (int64, error) {
	srcStat, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	normalizedRelPath := filepath.ToSlash(relPath)

	// Generate obfuscated name based on hash of path
	hash := sha256.Sum256([]byte(normalizedRelPath))
	encName := hex.EncodeToString(hash[:8]) + ".enc"
	dst := filepath.Join(ec.encDir, encName)

	// Stream-encrypt the file (skip sync for better USB performance)
	written, err := crypto.EncryptFileStreamWithOptions(ctx, src, dst, ec.encryptor, ec.skipSync)
	if err != nil {
		return 0, err
	}

	// Track file for batched sync if skipping per-file syncs
	if ec.skipSync {
		ec.syncMu.Lock()
		ec.syncFiles = append(ec.syncFiles, dst)
		ec.syncMu.Unlock()
	}

	// Add to manifest
	chunkSize := int64(crypto.DefaultChunkSize)
	chunkCount := int((srcStat.Size() + chunkSize - 1) / chunkSize)
	contentType := contentTypeForPath(normalizedRelPath)
	ec.mu.Lock()
	ec.manifest[normalizedRelPath] = manifestEntry{
		EncryptedName: encName,
		OriginalSize:  srcStat.Size(),
		ChunkCount:    chunkCount,
		ContentType:   contentType,
	}
	ec.mu.Unlock()

	return written, nil
}

// batchSyncFiles syncs all tracked files in parallel (for USB performance).
// This is called at the end of export instead of syncing after each file.
func (ec *encryptionContext) batchSyncFiles(ctx context.Context) error {
	ec.syncMu.Lock()
	files := make([]string, len(ec.syncFiles))
	copy(files, ec.syncFiles)
	ec.syncMu.Unlock()

	if len(files) == 0 {
		return nil
	}

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Sync files in parallel (up to 8 at a time to avoid overwhelming USB)
	const maxConcurrentSyncs = 8
	sem := make(chan struct{}, maxConcurrentSyncs)
	var wg sync.WaitGroup
	var syncErr error
	var syncErrMu sync.Mutex

	for _, filePath := range files {
		// Check for cancellation before each file
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore
		go func(path string) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			f, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				syncErrMu.Lock()
				if syncErr == nil {
					syncErr = err
				}
				syncErrMu.Unlock()
				return
			}
			err = f.Sync()
			f.Close()
			if err != nil {
				syncErrMu.Lock()
				if syncErr == nil {
					syncErr = err
				}
				syncErrMu.Unlock()
			}
		}(filePath)
	}

	wg.Wait()
	return syncErr
}

// writeManifestAndData writes the encrypted manifest and data files.
func (ec *encryptionContext) writeManifestAndData(destPath string, tweetsData []byte) error {
	// Encrypt and write tweets-data.json as data.enc
	encryptedData, err := ec.encryptor.Encrypt(tweetsData)
	if err != nil {
		return fmt.Errorf("encrypt tweets data: %w", err)
	}
	if err := writeFileSync(filepath.Join(destPath, "data.enc"), encryptedData, 0644); err != nil {
		return fmt.Errorf("write data.enc: %w", err)
	}

	// Encrypt and write manifest
	manifestData, err := json.Marshal(manifestFile{
		Version:   2,
		ChunkSize: crypto.DefaultChunkSize,
		Entries:   ec.manifest,
	})
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	encryptedManifest, err := ec.encryptor.Encrypt(manifestData)
	if err != nil {
		return fmt.Errorf("encrypt manifest: %w", err)
	}

	if err := writeFileSync(filepath.Join(destPath, "manifest.enc"), encryptedManifest, 0644); err != nil {
		return fmt.Errorf("write manifest.enc: %w", err)
	}

	return nil
}

// copyOfflineUI generates the offline-capable index.html.
// It creates a loader page that fetches tweets-data.json, sets OFFLINE_DATA,
// and then includes the main UI which will detect offline mode automatically.
func (s *ExportService) copyOfflineUI(destPath string) error {
	// Use the embedded shared UI (same UI as main server)
	// The HTML has offline mode support built-in via OFFLINE_DATA detection
	if len(ui.IndexHTML) > 0 {
		// Inject offline data loader script into the shared UI
		offlineHTML := injectOfflineDataLoader(string(ui.IndexHTML))
		s.logger.Info("writing index.html from shared UI", "path", destPath)
		return writeFileSync(filepath.Join(destPath, "index.html"), []byte(offlineHTML), 0644)
	}

	// Fallback: generate a standalone offline viewer if shared UI not available
	// This should never happen in normal builds but provides safety
	s.logger.Warn("shared UI not available, using standalone offline viewer")
	offlineHTML := generateOfflineHTML()
	return writeFileSync(filepath.Join(destPath, "index.html"), []byte(offlineHTML), 0644)
}

// injectOfflineDataLoader modifies the HTML to load tweets-data.json synchronously before main script
func injectOfflineDataLoader(html string) string {
	// Use synchronous XMLHttpRequest to ensure data is loaded before main script runs
	// This is intentionally synchronous to guarantee OFFLINE_DATA is available
	loaderScript := `<script>
    // Load offline data synchronously before main app initializes
    // Using sync XHR to ensure data is available when main script starts
    (function() {
        try {
            var xhr = new XMLHttpRequest();
            xhr.open('GET', 'tweets-data.json', false); // false = synchronous
            xhr.send(null);
            if (xhr.status === 200) {
                window.OFFLINE_DATA = JSON.parse(xhr.responseText);
                console.log('Loaded offline archive:', window.OFFLINE_DATA.total, 'tweets');
            } else {
                console.error('Failed to load tweets-data.json:', xhr.status);
                window.OFFLINE_DATA = { tweets: [], total: 0 };
            }
        } catch (error) {
            console.error('Failed to load tweets-data.json:', error);
            window.OFFLINE_DATA = { tweets: [], total: 0 };
        }
    })();
</script>
    <script>`

	// Replace the opening <script> tag with our loader + original script
	return strings.Replace(html, "    <script>", loaderScript, 1)
}

// copyViewerBinaries copies the cross-platform viewer binaries.
func (s *ExportService) copyViewerBinaries(binDir, destPath string) error {
	binaries := []struct {
		src  string
		dest string
	}{
		{"xgrabba-viewer.exe", "xgrabba-viewer.exe"},
		{"xgrabba-viewer-mac", "xgrabba-viewer-mac"},             // Universal binary if available
		{"xgrabba-viewer-mac-arm64", "xgrabba-viewer-mac-arm64"}, // Apple Silicon
		{"xgrabba-viewer-mac-amd64", "xgrabba-viewer-mac-amd64"}, // Intel Mac
		{"xgrabba-viewer-linux", "xgrabba-viewer-linux"},
	}

	for _, bin := range binaries {
		srcPath := filepath.Join(binDir, bin.src)
		srcStat, err := os.Stat(srcPath)
		if err != nil || srcStat.Size() == 0 {
			continue // Skip missing or empty binaries
		}

		dstPath := filepath.Join(destPath, bin.dest)
		if _, err := copyFile(srcPath, dstPath); err != nil {
			s.logger.Warn("failed to copy viewer binary", "src", bin.src, "error", err)
			continue
		}

		// Make executable on Unix
		if err := os.Chmod(dstPath, 0755); err != nil {
			s.logger.Warn("failed to chmod viewer binary", "dst", dstPath, "error", err)
		}
		s.logger.Info("copied viewer binary", "src", bin.src, "size", srcStat.Size())
	}

	return nil
}

// writeReadme writes the README.txt file.
func (s *ExportService) writeReadme(destPath string, tweetCount int, totalBytes int64) error {
	sizeStr := formatBytes(totalBytes)

	readme := fmt.Sprintf(`================================================================================
                         XGRABBA ARCHIVE EXPORT
================================================================================

ARCHIVE STATISTICS
------------------
Tweets Archived:  %d
Total Data Size:  %s
Export Date:      %s

================================================================================

QUICK START - VIEW YOUR ARCHIVE
================================

Choose your operating system below and follow the steps:


WINDOWS
-------
1. Double-click "xgrabba-viewer.exe"

2. If Windows SmartScreen appears saying "Windows protected your PC":
   - Click "More info"
   - Click "Run anyway"

3. Your default browser will open with your archive. Done!


MACOS (Apple Silicon - M1/M2/M3/M4)
-----------------------------------
1. Find "xgrabba-viewer-mac-arm64" in this folder

2. RIGHT-CLICK the file and select "Open" from the menu
   (Important: Don't double-click! You must right-click first)

3. A dialog will appear saying the app is from an unidentified developer:
   - Click "Open" to proceed
   - You only need to do this once

4. Your default browser will open with your archive. Done!

   Not sure if you have Apple Silicon? Click the Apple menu > "About This Mac"
   If it says "Chip: Apple M1/M2/M3/M4" you have Apple Silicon.


MACOS (Intel)
-------------
1. Find "xgrabba-viewer-mac-amd64" in this folder

2. RIGHT-CLICK the file and select "Open" from the menu
   (Important: Don't double-click! You must right-click first)

3. A dialog will appear saying the app is from an unidentified developer:
   - Click "Open" to proceed
   - You only need to do this once

4. Your default browser will open with your archive. Done!

   Not sure if you have Intel? Click the Apple menu > "About This Mac"
   If it says "Processor: Intel" you have an Intel Mac.


LINUX
-----
1. Open a terminal in this folder

2. Make the viewer executable and run it:
   chmod +x xgrabba-viewer-linux
   ./xgrabba-viewer-linux

3. Your default browser will open with your archive. Done!


================================================================================

ALTERNATIVE: JUST OPEN INDEX.HTML
---------------------------------
You can also simply double-click "index.html" to view in your browser.
Note: Some features may not work due to browser security restrictions.


ALTERNATIVE: USE PYTHON (Advanced)
----------------------------------
If you have Python installed:

1. Open a terminal/command prompt in this folder
2. Run: python -m http.server 8080
   (On some systems: python3 -m http.server 8080)
3. Open your browser to: http://localhost:8080


================================================================================

TROUBLESHOOTING
---------------

Windows - "Windows protected your PC" won't go away:
  → Make sure you clicked "More info" first, then "Run anyway"

macOS - "Cannot be opened because it is from an unidentified developer":
  → You must RIGHT-CLICK and select "Open", not double-click

macOS - Still blocked after right-clicking:
  → Go to System Settings > Privacy & Security > scroll down
  → Click "Open Anyway" next to the blocked app

Linux - "Permission denied":
  → Run: chmod +x xgrabba-viewer-linux

Browser shows blank page:
  → Try a different browser (Chrome, Firefox, Safari, Edge)
  → Make sure tweets-data.json exists in this folder


================================================================================

WHAT'S IN THIS ARCHIVE
----------------------

README.txt               - This file (you're reading it!)
index.html               - Web-based archive viewer
tweets-data.json         - All tweet data in JSON format

xgrabba-viewer.exe       - Windows viewer app
xgrabba-viewer-mac-arm64 - macOS viewer (Apple Silicon M1/M2/M3/M4)
xgrabba-viewer-mac-amd64 - macOS viewer (Intel Macs)
xgrabba-viewer-linux     - Linux viewer app

data/                    - Your archived tweets organized by date
  └── YYYY/MM/           - Year and month folders
      └── username_date_id/
          ├── tweet.json     - Tweet metadata
          ├── README.md      - Human-readable summary
          ├── avatar.jpg     - Author's profile picture
          └── media/         - Images, videos, thumbnails


================================================================================

LEGAL DISCLAIMER
----------------

XGrabba is FREE, OPEN-SOURCE software for personal archival purposes.

THE SOFTWARE IS PROVIDED "AS IS" WITHOUT WARRANTY OF ANY KIND.

The developers and contributors of XGrabba:
  • Are NOT responsible for the content you archive
  • Are NOT responsible for any data loss or corruption
  • Do NOT guarantee the accuracy or completeness of archived content
  • Do NOT provide technical support or data recovery services

YOU are responsible for:
  • Having the right to archive the content you save
  • Following applicable laws and platform terms of service
  • Keeping backups of your important data
  • The security and privacy of your archived data

By using this archive, you accept these terms.


================================================================================

ABOUT XGRABBA
-------------

XGrabba is an open-source tweet archival tool.

Website:  https://github.com/iconidentify/xgrabba
License:  MIT License (free to use and modify)
Issues:   https://github.com/iconidentify/xgrabba/issues

================================================================================
`, tweetCount, sizeStr, time.Now().Format("January 2, 2006 at 3:04:05 PM MST"))

	return writeFileSync(filepath.Join(destPath, "README.txt"), []byte(readme), 0644)
}

// formatBytes converts bytes to human-readable format.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func contentTypeForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return "application/json"
	case ".js":
		return "application/javascript"
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	default:
		return "application/octet-stream"
	}
}

// generateOfflineHTML generates the offline viewer HTML.
// This is a simplified version that will be replaced with a modified index.html.
func generateOfflineHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>XGrabba Archive Viewer</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background: #000;
            color: #e7e9ea;
            line-height: 1.5;
        }
        .container { max-width: 1200px; margin: 0 auto; padding: 20px; }
        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 20px 0;
            border-bottom: 1px solid #2f3336;
            margin-bottom: 20px;
        }
        .header h1 { font-size: 24px; font-weight: 700; }
        .stats { color: #71767b; font-size: 14px; }
        .search-box {
            width: 100%;
            padding: 12px 16px;
            background: #202327;
            border: 1px solid #2f3336;
            border-radius: 9999px;
            color: #e7e9ea;
            font-size: 15px;
            margin-bottom: 20px;
        }
        .search-box:focus { outline: none; border-color: #1d9bf0; }
        .grid {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
            gap: 16px;
        }
        .tweet-card {
            background: #16181c;
            border: 1px solid #2f3336;
            border-radius: 16px;
            overflow: hidden;
            cursor: pointer;
            transition: background 0.2s;
        }
        .tweet-card:hover { background: #1d1f23; }
        .tweet-media {
            width: 100%;
            aspect-ratio: 16/9;
            object-fit: cover;
            background: #202327;
        }
        .tweet-content { padding: 12px; }
        .tweet-author {
            display: flex;
            align-items: center;
            gap: 8px;
            margin-bottom: 8px;
        }
        .avatar {
            width: 40px;
            height: 40px;
            border-radius: 50%;
            background: #2f3336;
        }
        .author-info { flex: 1; }
        .author-name { font-weight: 700; font-size: 15px; }
        .author-handle { color: #71767b; font-size: 14px; }
        .tweet-text {
            font-size: 15px;
            margin-bottom: 8px;
            display: -webkit-box;
            -webkit-line-clamp: 3;
            -webkit-box-orient: vertical;
            overflow: hidden;
        }
        .tweet-title {
            font-size: 13px;
            color: #1d9bf0;
            margin-bottom: 4px;
        }
        .tweet-tags {
            display: flex;
            flex-wrap: wrap;
            gap: 4px;
            margin-top: 8px;
        }
        .tag {
            background: #1d9bf0;
            color: #fff;
            padding: 2px 8px;
            border-radius: 9999px;
            font-size: 12px;
        }
        .modal {
            display: none;
            position: fixed;
            top: 0;
            left: 0;
            width: 100%;
            height: 100%;
            background: rgba(0,0,0,0.9);
            z-index: 1000;
            overflow-y: auto;
        }
        .modal.active { display: block; }
        .modal-content {
            max-width: 800px;
            margin: 40px auto;
            background: #16181c;
            border-radius: 16px;
            overflow: hidden;
        }
        .modal-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 16px;
            border-bottom: 1px solid #2f3336;
        }
        .modal-close {
            background: none;
            border: none;
            color: #e7e9ea;
            font-size: 24px;
            cursor: pointer;
        }
        .modal-media {
            width: 100%;
            max-height: 500px;
            object-fit: contain;
            background: #000;
        }
        .modal-body { padding: 16px; }
        .full-text { font-size: 16px; white-space: pre-wrap; margin-bottom: 16px; }
        .metrics {
            display: flex;
            gap: 16px;
            color: #71767b;
            font-size: 14px;
            margin-top: 12px;
        }
        .loading {
            text-align: center;
            padding: 40px;
            color: #71767b;
        }
        .no-results {
            text-align: center;
            padding: 60px 20px;
            color: #71767b;
        }
        .transcript {
            background: #202327;
            padding: 12px;
            border-radius: 8px;
            margin-top: 12px;
            font-size: 14px;
            max-height: 200px;
            overflow-y: auto;
        }
        .transcript-label {
            font-size: 12px;
            color: #71767b;
            margin-bottom: 4px;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>XGrabba Archive</h1>
            <div class="stats" id="stats">Loading...</div>
        </div>
        <input type="text" class="search-box" id="search" placeholder="Search tweets, authors, tags...">
        <div class="grid" id="grid"></div>
        <div class="loading" id="loading">Loading archive...</div>
        <div class="no-results" id="no-results" style="display:none;">No tweets found</div>
    </div>

    <div class="modal" id="modal">
        <div class="modal-content">
            <div class="modal-header">
                <span id="modal-title"></span>
                <button class="modal-close" onclick="closeModal()">&times;</button>
            </div>
            <div id="modal-media-container"></div>
            <div class="modal-body" id="modal-body"></div>
        </div>
    </div>

    <script>
        let allTweets = [];
        let filteredTweets = [];

        async function loadData() {
            try {
                const response = await fetch('tweets-data.json');
                const data = await response.json();
                allTweets = data.tweets || [];
                filteredTweets = allTweets;

                document.getElementById('stats').textContent = allTweets.length + ' tweets';
                document.getElementById('loading').style.display = 'none';

                renderTweets();
            } catch (error) {
                document.getElementById('loading').textContent = 'Error loading archive: ' + error.message;
            }
        }

        function renderTweets() {
            const grid = document.getElementById('grid');
            const noResults = document.getElementById('no-results');

            if (filteredTweets.length === 0) {
                grid.innerHTML = '';
                noResults.style.display = 'block';
                return;
            }

            noResults.style.display = 'none';
            grid.innerHTML = filteredTweets.map((tweet, index) => {
                const media = tweet.media && tweet.media[0];
                let mediaHtml = '';

                if (media) {
                    if (media.thumbnail_path) {
                        mediaHtml = '<img class="tweet-media" src="' + media.thumbnail_path + '" alt="">';
                    } else if (media.local_path && media.type === 'image') {
                        mediaHtml = '<img class="tweet-media" src="' + media.local_path + '" alt="">';
                    }
                }

                const tags = (tweet.ai_tags || []).slice(0, 3).map(t =>
                    '<span class="tag">' + escapeHtml(t) + '</span>'
                ).join('');

                return '<div class="tweet-card" onclick="openModal(' + index + ')">' +
                    mediaHtml +
                    '<div class="tweet-content">' +
                        '<div class="tweet-author">' +
                            (tweet.author.avatar_path ?
                                '<img class="avatar" src="' + tweet.author.avatar_path + '" alt="">' :
                                '<div class="avatar"></div>') +
                            '<div class="author-info">' +
                                '<div class="author-name">' + escapeHtml(tweet.author.display_name) + '</div>' +
                                '<div class="author-handle">@' + escapeHtml(tweet.author.username) + '</div>' +
                            '</div>' +
                        '</div>' +
                        (tweet.ai_title ? '<div class="tweet-title">' + escapeHtml(tweet.ai_title) + '</div>' : '') +
                        '<div class="tweet-text">' + escapeHtml(tweet.text) + '</div>' +
                        (tags ? '<div class="tweet-tags">' + tags + '</div>' : '') +
                    '</div>' +
                '</div>';
            }).join('');
        }

        function openModal(index) {
            const tweet = filteredTweets[index];
            const modal = document.getElementById('modal');
            const title = document.getElementById('modal-title');
            const mediaContainer = document.getElementById('modal-media-container');
            const body = document.getElementById('modal-body');

            title.textContent = tweet.ai_title || 'Tweet Details';

            // Media
            let mediaHtml = '';
            if (tweet.media && tweet.media.length > 0) {
                const media = tweet.media[0];
                if (media.type === 'video' || media.type === 'gif') {
                    mediaHtml = '<video class="modal-media" controls src="' + media.local_path + '"></video>';
                } else if (media.type === 'image') {
                    mediaHtml = '<img class="modal-media" src="' + media.local_path + '" alt="">';
                }
            }
            mediaContainer.innerHTML = mediaHtml;

            // Body
            let bodyHtml = '<div class="tweet-author">' +
                (tweet.author.avatar_path ?
                    '<img class="avatar" src="' + tweet.author.avatar_path + '" alt="">' :
                    '<div class="avatar"></div>') +
                '<div class="author-info">' +
                    '<div class="author-name">' + escapeHtml(tweet.author.display_name) + '</div>' +
                    '<div class="author-handle">@' + escapeHtml(tweet.author.username) + '</div>' +
                '</div>' +
            '</div>' +
            '<div class="full-text">' + escapeHtml(tweet.text) + '</div>';

            if (tweet.ai_summary) {
                bodyHtml += '<div style="color:#71767b;font-size:14px;margin-bottom:12px;">AI Summary: ' + escapeHtml(tweet.ai_summary) + '</div>';
            }

            // Transcript
            const media = tweet.media && tweet.media[0];
            if (media && media.transcript) {
                bodyHtml += '<div class="transcript">' +
                    '<div class="transcript-label">Transcript' + (media.transcript_language ? ' (' + media.transcript_language + ')' : '') + '</div>' +
                    escapeHtml(media.transcript) +
                '</div>';
            }

            // Tags
            const allTags = (tweet.ai_tags || []).concat(
                (tweet.media || []).flatMap(m => m.ai_tags || [])
            );
            if (allTags.length > 0) {
                bodyHtml += '<div class="tweet-tags" style="margin-top:12px;">' +
                    allTags.slice(0, 10).map(t => '<span class="tag">' + escapeHtml(t) + '</span>').join('') +
                '</div>';
            }

            bodyHtml += '<div class="metrics">' +
                '<span>' + (tweet.metrics.likes || 0) + ' likes</span>' +
                '<span>' + (tweet.metrics.retweets || 0) + ' retweets</span>' +
                '<span>' + (tweet.metrics.replies || 0) + ' replies</span>' +
            '</div>';

            body.innerHTML = bodyHtml;
            modal.classList.add('active');
        }

        function closeModal() {
            const modal = document.getElementById('modal');
            modal.classList.remove('active');
            // Stop video if playing
            const video = modal.querySelector('video');
            if (video) video.pause();
        }

        function search(query) {
            query = query.toLowerCase().trim();
            if (!query) {
                filteredTweets = allTweets;
            } else {
                filteredTweets = allTweets.filter(tweet => {
                    if (tweet.text.toLowerCase().includes(query)) return true;
                    if (tweet.author.username.toLowerCase().includes(query)) return true;
                    if (tweet.author.display_name.toLowerCase().includes(query)) return true;
                    if ((tweet.ai_title || '').toLowerCase().includes(query)) return true;
                    if ((tweet.ai_summary || '').toLowerCase().includes(query)) return true;
                    if ((tweet.ai_tags || []).some(t => t.toLowerCase().includes(query))) return true;
                    if ((tweet.ai_topics || []).some(t => t.toLowerCase().includes(query))) return true;
                    for (const media of (tweet.media || [])) {
                        if ((media.transcript || '').toLowerCase().includes(query)) return true;
                        if ((media.ai_caption || '').toLowerCase().includes(query)) return true;
                        if ((media.ai_tags || []).some(t => t.toLowerCase().includes(query))) return true;
                    }
                    return false;
                });
            }
            renderTweets();
        }

        function escapeHtml(text) {
            if (!text) return '';
            return text
                .replace(/&/g, '&amp;')
                .replace(/</g, '&lt;')
                .replace(/>/g, '&gt;')
                .replace(/"/g, '&quot;')
                .replace(/'/g, '&#39;');
        }

        // Event listeners
        document.getElementById('search').addEventListener('input', (e) => search(e.target.value));
        document.getElementById('modal').addEventListener('click', (e) => {
            if (e.target.id === 'modal') closeModal();
        });
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') closeModal();
        });

        // Load data on page load
        loadData();
    </script>
</body>
</html>`
}

// exportStatePath returns the path to the persisted export state file.
func (s *ExportService) exportStatePath() string {
	if s.storagePath == "" {
		return ""
	}
	return filepath.Join(s.storagePath, ".export_state.json")
}

// saveExportState persists the current export state to disk.
func (s *ExportService) saveExportState() error {
	if s.storagePath == "" {
		return nil // No persistence if no storage path
	}

	s.mu.Lock()
	export := s.activeExport
	s.mu.Unlock()

	if export == nil {
		// No active export, remove state file if it exists
		statePath := s.exportStatePath()
		if _, err := os.Stat(statePath); err == nil {
			os.Remove(statePath)
		}
		return nil
	}

	// Create a serializable copy (without cancelFunc)
	state := struct {
		ID             string    `json:"export_id"`
		DestPath       string    `json:"dest_path"`
		MountPoint     string    `json:"mount_point,omitempty"`
		Phase          string    `json:"phase"`
		TotalTweets    int       `json:"total_tweets"`
		ExportedTweets int       `json:"exported_tweets"`
		BytesWritten   int64     `json:"bytes_written"`
		CurrentFile    string    `json:"current_file"`
		StartedAt      time.Time `json:"started_at"`
		Error          string    `json:"error,omitempty"`
		ZipPath        string    `json:"zip_path,omitempty"`
	}{
		ID:             export.ID,
		DestPath:       export.DestPath,
		MountPoint:     export.MountPoint,
		Phase:          export.Phase,
		TotalTweets:    export.TotalTweets,
		ExportedTweets: export.ExportedTweets,
		BytesWritten:   export.BytesWritten,
		CurrentFile:    export.CurrentFile,
		StartedAt:      export.StartedAt,
		Error:          export.Error,
		ZipPath:        export.ZipPath,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal export state: %w", err)
	}

	statePath := s.exportStatePath()
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0644); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}

	return nil
}

// loadExportState loads persisted export state from disk.
func (s *ExportService) loadExportState() error {
	if s.storagePath == "" {
		return nil
	}

	statePath := s.exportStatePath()
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No state file, nothing to load
		}
		return fmt.Errorf("read state file: %w", err)
	}

	var state struct {
		ID             string    `json:"export_id"`
		DestPath       string    `json:"dest_path"`
		MountPoint     string    `json:"mount_point,omitempty"`
		Phase          string    `json:"phase"`
		TotalTweets    int       `json:"total_tweets"`
		ExportedTweets int       `json:"exported_tweets"`
		BytesWritten   int64     `json:"bytes_written"`
		CurrentFile    string    `json:"current_file"`
		StartedAt      time.Time `json:"started_at"`
		Error          string    `json:"error,omitempty"`
		ZipPath        string    `json:"zip_path,omitempty"`
	}

	if err := json.Unmarshal(data, &state); err != nil {
		s.logger.Warn("failed to parse export state, removing corrupted file", "error", err, "path", statePath)
		os.Remove(statePath)
		return nil
	}

	// Only restore if export was in progress (not completed/failed/cancelled)
	if state.Phase == "preparing" || state.Phase == "exporting" || state.Phase == "finalizing" {
		s.mu.Lock()
		s.activeExport = &ActiveExport{
			ID:             state.ID,
			DestPath:       state.DestPath,
			MountPoint:     state.MountPoint,
			Phase:          "failed", // Mark as failed since we can't resume
			TotalTweets:    state.TotalTweets,
			ExportedTweets: state.ExportedTweets,
			BytesWritten:   state.BytesWritten,
			CurrentFile:    state.CurrentFile,
			StartedAt:      state.StartedAt,
			Error:          "Export interrupted by pod restart - cannot resume",
			ZipPath:        state.ZipPath,
		}
		s.mu.Unlock()

		s.logger.Info("restored export state from previous session",
			"export_id", state.ID,
			"phase", state.Phase,
			"dest_path", state.DestPath,
			"exported_tweets", state.ExportedTweets,
			"total_tweets", state.TotalTweets)

		// Emit event about the interrupted export
		s.emitEvent(domain.EventSeverityWarning, domain.EventCategoryExport,
			fmt.Sprintf("Previous export interrupted by restart: %s", state.CurrentFile),
			domain.EventMetadata{
				"export_id":       state.ID,
				"dest_path":       state.DestPath,
				"exported_tweets": state.ExportedTweets,
				"total_tweets":    state.TotalTweets,
			})
	}

	return nil
}

// SaveExportStateOnShutdown saves export state during graceful shutdown.
// This is called before the server shuts down to ensure state is persisted.
func (s *ExportService) SaveExportStateOnShutdown() {
	s.mu.Lock()
	hasActiveExport := s.activeExport != nil
	var exportID, phase, destPath string
	if s.activeExport != nil {
		exportID = s.activeExport.ID
		phase = s.activeExport.Phase
		destPath = s.activeExport.DestPath
	}
	s.mu.Unlock()

	if hasActiveExport {
		s.logger.Info("saving export state on shutdown",
			"export_id", exportID,
			"phase", phase,
			"dest_path", destPath)

		if err := s.saveExportState(); err != nil {
			s.logger.Error("failed to save export state on shutdown", "error", err)
		} else {
			s.logger.Info("export state saved successfully")
		}
	}
}
