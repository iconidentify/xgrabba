package service

// This file provides examples of how to integrate event emission into existing services.
// These are code snippets showing the pattern, not complete implementations.

/*
=============================================================================
INTEGRATION PATTERN FOR TWEET SERVICE
=============================================================================

The TweetService can emit events during the archival workflow.
Add an eventEmitter field and use it at key points:

type TweetService struct {
    // ... existing fields ...
    eventEmitter domain.EventEmitter  // Add this field
}

func NewTweetService(
    // ... existing params ...
    eventEmitter domain.EventEmitter,  // Add this parameter
) *TweetService {
    svc := &TweetService{
        // ... existing initialization ...
        eventEmitter: eventEmitter,
    }
    return svc
}

// In Archive method:
func (s *TweetService) Archive(ctx context.Context, req ArchiveRequest) (*ArchiveResponse, error) {
    s.eventEmitter.EmitInfo(domain.EventCategoryTweet, "TweetService",
        fmt.Sprintf("Archive requested: %s", req.TweetURL),
        domain.EventMetadata{"tweet_url": req.TweetURL})

    // ... existing logic ...

    if err != nil {
        s.eventEmitter.EmitError(domain.EventCategoryTweet, "TweetService",
            fmt.Sprintf("Archive failed: %v", err),
            domain.EventMetadata{"tweet_url": req.TweetURL, "error": err.Error()})
        return nil, err
    }

    s.eventEmitter.EmitSuccess(domain.EventCategoryTweet, "TweetService",
        fmt.Sprintf("Tweet queued: %s", tweetID),
        domain.EventMetadata{"tweet_id": string(tweetID)})
    return result, nil
}

// In processPhase1Fetch:
func (s *TweetService) processPhase1Fetch(ctx context.Context, tweet *domain.Tweet) error {
    s.eventEmitter.EmitInfo(domain.EventCategoryTweet, "TweetService.Phase1",
        fmt.Sprintf("Fetching tweet %s", tweet.ID),
        domain.EventMetadata{"tweet_id": string(tweet.ID)})

    // ... fetch logic ...

    if err != nil {
        s.eventEmitter.EmitError(domain.EventCategoryTweet, "TweetService.Phase1",
            fmt.Sprintf("Fetch failed: %v", err),
            domain.EventMetadata{"tweet_id": string(tweet.ID), "error": err.Error()})
        return err
    }

    s.eventEmitter.EmitSuccess(domain.EventCategoryTweet, "TweetService.Phase1",
        fmt.Sprintf("Tweet fetched: @%s", tweet.Author.Username),
        domain.EventMetadata{
            "tweet_id":    string(tweet.ID),
            "author":      tweet.Author.Username,
            "media_count": len(tweet.Media),
        })
    return nil
}

=============================================================================
INTEGRATION PATTERN FOR EXPORT SERVICE
=============================================================================

type ExportService struct {
    // ... existing fields ...
    eventEmitter domain.EventEmitter
}

// In runExportAsync:
func (s *ExportService) runExportAsync(ctx context.Context, opts ExportOptions) {
    s.eventEmitter.EmitInfo(domain.EventCategoryExport, "ExportService",
        fmt.Sprintf("Export started to %s", opts.DestPath),
        domain.EventMetadata{"dest_path": opts.DestPath, "encrypt": opts.Encrypt})

    // ... export tweets loop ...
    for i, tweet := range tweets {
        // Progress event every 10 tweets
        if i > 0 && i%10 == 0 {
            s.eventEmitter.EmitInfo(domain.EventCategoryExport, "ExportService",
                fmt.Sprintf("Export progress: %d/%d tweets", i, len(tweets)),
                domain.EventMetadata{
                    "exported": i,
                    "total":    len(tweets),
                    "percent":  (i * 100) / len(tweets),
                })
        }
    }

    if opts.Encrypt {
        s.eventEmitter.EmitInfo(domain.EventCategoryEncryption, "ExportService",
            "Starting encryption",
            domain.EventMetadata{"dest_path": opts.DestPath})

        if err := s.encryptExport(opts.DestPath, opts.Password); err != nil {
            s.eventEmitter.EmitError(domain.EventCategoryEncryption, "ExportService",
                fmt.Sprintf("Encryption failed: %v", err),
                domain.EventMetadata{"error": err.Error()})
            return
        }

        s.eventEmitter.EmitSuccess(domain.EventCategoryEncryption, "ExportService",
            "Encryption complete",
            domain.EventMetadata{"dest_path": opts.DestPath})
    }

    s.eventEmitter.EmitSuccess(domain.EventCategoryExport, "ExportService",
        fmt.Sprintf("Export complete: %d tweets", len(exportedTweets)),
        domain.EventMetadata{
            "dest_path":   opts.DestPath,
            "tweet_count": len(exportedTweets),
            "bytes":       totalBytes,
        })
}

=============================================================================
INTEGRATION PATTERN FOR BOOKMARKS MONITOR
=============================================================================

type Monitor struct {
    // ... existing fields ...
    eventEmitter domain.EventEmitter
}

// In pollOnce:
func (m *Monitor) pollOnce(ctx context.Context) (bool, bool) {
    ids, _, err := m.client.ListBookmarks(ctx, m.cfg.UserID, m.cfg.MaxResults, "")
    if err != nil {
        var rl *twitter.RateLimitError
        if errors.As(err, &rl) {
            m.eventEmitter.EmitWarning(domain.EventCategoryBookmarks, "BookmarksMonitor",
                fmt.Sprintf("Rate limited, backing off until %s", rl.Reset.Format(time.RFC3339)),
                domain.EventMetadata{"reset_at": rl.Reset.Format(time.RFC3339)})
            // ... handle rate limit ...
            return false, true
        }
        m.eventEmitter.EmitError(domain.EventCategoryBookmarks, "BookmarksMonitor",
            fmt.Sprintf("Poll failed: %v", err),
            domain.EventMetadata{"error": err.Error()})
        return false, false
    }

    if len(newIDs) > 0 {
        m.eventEmitter.EmitInfo(domain.EventCategoryBookmarks, "BookmarksMonitor",
            fmt.Sprintf("Found %d new bookmarks", len(newIDs)),
            domain.EventMetadata{"new_count": len(newIDs), "total_scanned": len(ids)})

        for _, id := range newIDs {
            // Archive each new bookmark
            if _, err := m.arch.Archive(ctx, service.ArchiveRequest{TweetURL: tweetURL}); err != nil {
                m.eventEmitter.EmitError(domain.EventCategoryBookmarks, "BookmarksMonitor",
                    fmt.Sprintf("Failed to archive bookmark %s", id),
                    domain.EventMetadata{"tweet_id": id, "error": err.Error()})
            } else {
                m.eventEmitter.EmitSuccess(domain.EventCategoryBookmarks, "BookmarksMonitor",
                    fmt.Sprintf("Bookmark archived: %s", id),
                    domain.EventMetadata{"tweet_id": id})
            }
        }
    }

    return true, false
}

=============================================================================
INTEGRATION PATTERN FOR USB HANDLER
=============================================================================

type USBHandler struct {
    // ... existing fields ...
    eventEmitter domain.EventEmitter
}

// When a drive is detected:
func (h *USBHandler) onDriveDetected(drive *usbmanager.DriveInfo) {
    h.eventEmitter.EmitInfo(domain.EventCategoryUSB, "USBHandler",
        fmt.Sprintf("Drive detected: %s (%s)", drive.Label, drive.Device),
        domain.EventMetadata{
            "device":     drive.Device,
            "label":      drive.Label,
            "filesystem": drive.Filesystem,
            "size_bytes": drive.Size,
        })
}

// When formatting starts:
func (h *USBHandler) FormatDriveAsync(w http.ResponseWriter, r *http.Request) {
    h.eventEmitter.EmitInfo(domain.EventCategoryUSB, "USBHandler",
        fmt.Sprintf("Format started: %s", device),
        domain.EventMetadata{"device": device, "filesystem": req.Filesystem})

    // ... start format operation ...
}

// When formatting completes:
func (h *USBHandler) onFormatComplete(device, label string, err error) {
    if err != nil {
        h.eventEmitter.EmitError(domain.EventCategoryUSB, "USBHandler",
            fmt.Sprintf("Format failed: %v", err),
            domain.EventMetadata{"device": device, "error": err.Error()})
    } else {
        h.eventEmitter.EmitSuccess(domain.EventCategoryUSB, "USBHandler",
            fmt.Sprintf("Format complete: %s", label),
            domain.EventMetadata{"device": device, "label": label})
    }
}

=============================================================================
INTEGRATION PATTERN FOR AI OPERATIONS (GROK/WHISPER)
=============================================================================

// In runVisionAnalysis:
func (s *TweetService) runVisionAnalysis(ctx context.Context, tweet *domain.Tweet) {
    s.eventEmitter.EmitInfo(domain.EventCategoryAI, "TweetService.VisionAnalysis",
        fmt.Sprintf("Starting vision analysis for %s", tweet.ID),
        domain.EventMetadata{
            "tweet_id":     string(tweet.ID),
            "image_count":  len(imagePaths),
            "has_keyframes": len(keyframePaths) > 0,
        })

    analysis, err := s.grokClient.AnalyzeContentWithVision(ctx, req)
    if err != nil {
        s.eventEmitter.EmitError(domain.EventCategoryAI, "TweetService.VisionAnalysis",
            fmt.Sprintf("Vision analysis failed: %v", err),
            domain.EventMetadata{"tweet_id": string(tweet.ID), "error": err.Error()})
        // Fall back to text analysis
        s.runTextAnalysis(ctx, tweet)
        return
    }

    s.eventEmitter.EmitSuccess(domain.EventCategoryAI, "TweetService.VisionAnalysis",
        fmt.Sprintf("Vision analysis complete: %d tags", len(analysis.Tags)),
        domain.EventMetadata{
            "tweet_id":     string(tweet.ID),
            "tags_count":   len(analysis.Tags),
            "content_type": analysis.ContentType,
        })
}

// In processVideoForTranscription:
func (s *TweetService) processVideoForTranscription(ctx context.Context, media *domain.Media, archivePath string) {
    s.eventEmitter.EmitInfo(domain.EventCategoryAI, "TweetService.Whisper",
        fmt.Sprintf("Starting transcription for %s", media.ID),
        domain.EventMetadata{"media_id": media.ID})

    transcription, err := s.whisperClient.TranscribeFile(ctx, audioPath, whisper.TranscriptionOptions{})
    if err != nil {
        s.eventEmitter.EmitError(domain.EventCategoryAI, "TweetService.Whisper",
            fmt.Sprintf("Transcription failed: %v", err),
            domain.EventMetadata{"media_id": media.ID, "error": err.Error()})
        return
    }

    s.eventEmitter.EmitSuccess(domain.EventCategoryAI, "TweetService.Whisper",
        fmt.Sprintf("Transcription complete: %d chars, language: %s", len(transcription.Text), transcription.Language),
        domain.EventMetadata{
            "media_id":   media.ID,
            "text_len":   len(transcription.Text),
            "language":   transcription.Language,
        })
}

=============================================================================
DISK SPACE MONITORING INTEGRATION
=============================================================================

// Add a background goroutine to check disk space periodically:
func (s *TweetService) startDiskSpaceMonitor(ctx context.Context, eventEmitter domain.EventEmitter) {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()

    warningThreshold := int64(5 * 1024 * 1024 * 1024)  // 5GB
    criticalThreshold := int64(1 * 1024 * 1024 * 1024) // 1GB

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            free := getFreeDiskSpace(s.cfg.BasePath)
            if free < criticalThreshold {
                eventEmitter.EmitError(domain.EventCategoryDisk, "DiskMonitor",
                    fmt.Sprintf("Critical: Only %s free disk space", formatBytes(free)),
                    domain.EventMetadata{
                        "path":       s.cfg.BasePath,
                        "free_bytes": free,
                        "threshold":  "critical",
                    })
            } else if free < warningThreshold {
                eventEmitter.EmitWarning(domain.EventCategoryDisk, "DiskMonitor",
                    fmt.Sprintf("Warning: Only %s free disk space", formatBytes(free)),
                    domain.EventMetadata{
                        "path":       s.cfg.BasePath,
                        "free_bytes": free,
                        "threshold":  "warning",
                    })
            }
        }
    }
}

=============================================================================
NULL EVENT EMITTER FOR BACKWARD COMPATIBILITY
=============================================================================

If you need to make event emission optional (for backward compatibility),
use this null implementation:

*/

import "github.com/iconidentify/xgrabba/internal/domain"

// NullEventEmitter is a no-op implementation of EventEmitter.
// Use this when event emission is not needed or for testing.
type NullEventEmitter struct{}

// Ensure NullEventEmitter implements domain.EventEmitter
var _ domain.EventEmitter = (*NullEventEmitter)(nil)

func (e *NullEventEmitter) Emit(event domain.Event) {}

func (e *NullEventEmitter) EmitInfo(category domain.EventCategory, source, message string, metadata domain.EventMetadata) {
}

func (e *NullEventEmitter) EmitWarning(category domain.EventCategory, source, message string, metadata domain.EventMetadata) {
}

func (e *NullEventEmitter) EmitError(category domain.EventCategory, source, message string, metadata domain.EventMetadata) {
}

func (e *NullEventEmitter) EmitSuccess(category domain.EventCategory, source, message string, metadata domain.EventMetadata) {
}

// NewNullEventEmitter creates a no-op event emitter.
func NewNullEventEmitter() *NullEventEmitter {
	return &NullEventEmitter{}
}
