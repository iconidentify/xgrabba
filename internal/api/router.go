package api

import (
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/iconidentify/xgrabba/internal/api/handler"
	mw "github.com/iconidentify/xgrabba/internal/api/middleware"
)

// NewRouter creates the HTTP router with all routes configured.
func NewRouter(
	videoHandler *handler.VideoHandler,
	tweetHandler *handler.TweetHandler,
	healthHandler *handler.HealthHandler,
	uiHandler *handler.UIHandler,
	exportHandler *handler.ExportHandler,
	bookmarksOAuthHandler *handler.BookmarksOAuthHandler,
	usbHandler *handler.USBHandler,
	eventHandler *handler.EventHandler,
	extensionHandler *handler.ExtensionHandler,
	apiKey string,
) *chi.Mux {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.CleanPath) // Normalize paths (e.g., //ready -> /ready)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(mw.Logger)
	r.Use(mw.Recovery)
	r.Use(middleware.Timeout(5 * time.Minute))

	// CORS for browser extension
	r.Use(mw.CORS)

	// Health endpoints (no auth)
	r.Get("/health", healthHandler.Live)
	r.Get("/ready", healthHandler.Ready)

	// Web UI (no auth - authentication handled via API key in UI)
	r.Get("/", uiHandler.Smart)          // Auto-detect mobile vs desktop
	r.Get("/ui", uiHandler.Index)        // Full archive browser
	r.Get("/quick", uiHandler.Quick)     // Mobile-optimized quick archive
	r.Get("/q", uiHandler.Quick)         // Short alias for mobile
	r.Get("/admin/events", uiHandler.AdminEvents) // Admin activity log

	// OAuth callback must be unauthenticated because it's a browser redirect from X.
	if bookmarksOAuthHandler != nil {
		r.Get("/bookmarks/oauth/start", bookmarksOAuthHandler.Start)
		r.Get("/bookmarks/oauth/callback", bookmarksOAuthHandler.Callback)
	}

	// API v1 (authenticated)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(mw.APIKeyAuth(apiKey))

		// System stats
		r.Get("/stats", healthHandler.Stats)

		// Bookmarks OAuth connect flow and monitor control (optional)
		if bookmarksOAuthHandler != nil {
			r.Get("/bookmarks/oauth/status", bookmarksOAuthHandler.Status)
			r.Post("/bookmarks/oauth/disconnect", bookmarksOAuthHandler.Disconnect)
			// Enhanced status and monitor control
			r.Get("/bookmarks/status", bookmarksOAuthHandler.EnhancedStatus)
			r.Get("/bookmarks/activity", bookmarksOAuthHandler.Activity)
			// Permanent-failure cache visibility/reset
			r.Get("/bookmarks/failed-cache", bookmarksOAuthHandler.FailedCacheStatus)
			r.Post("/bookmarks/failed-cache/clear", bookmarksOAuthHandler.ClearFailedCache)
			r.Post("/bookmarks/pause", bookmarksOAuthHandler.PauseMonitor)
			r.Post("/bookmarks/resume", bookmarksOAuthHandler.ResumeMonitor)
			r.Post("/bookmarks/check-now", bookmarksOAuthHandler.CheckNowMonitor)
		}

		// Tweet operations (new - full tweet archival)
		r.Post("/tweets", tweetHandler.Archive)
		r.Get("/tweets", tweetHandler.List)
		r.Post("/tweets/batch-status", tweetHandler.BatchStatus)           // Batch status polling for UI
		r.Get("/tweets/search", tweetHandler.Search)                       // Search tweets with pagination
		r.Get("/tweets/truncated", tweetHandler.ListTruncated)             // List tweets with truncated text
		r.Post("/tweets/backfill-truncated", tweetHandler.BackfillTruncated) // Backfill all truncated tweets
		r.Get("/tweets/{tweetID}", tweetHandler.Get)
		r.Get("/tweets/{tweetID}/status", tweetHandler.GetStatus)
		r.Get("/tweets/{tweetID}/full", tweetHandler.GetFull)
		r.Get("/tweets/{tweetID}/media", tweetHandler.ListMedia)
		r.Get("/tweets/{tweetID}/media/{filename}", tweetHandler.ServeMedia)
		r.Get("/tweets/{tweetID}/avatar", tweetHandler.ServeAvatar)
		r.Delete("/tweets/{tweetID}", tweetHandler.Delete)
		r.Post("/tweets/{tweetID}/regenerate-ai", tweetHandler.RegenerateAI)
		r.Post("/tweets/{tweetID}/resync", tweetHandler.Resync)
		r.Get("/tweets/{tweetID}/ai-status", tweetHandler.CheckAIAnalysisStatus)
		r.Get("/tweets/{tweetID}/diagnostics", tweetHandler.GetDiagnostics)

		// Video operations (legacy - kept for backwards compatibility)
		r.Post("/videos", videoHandler.Submit)
		r.Get("/videos", videoHandler.List)
		r.Get("/videos/{videoID}", videoHandler.Get)
		r.Get("/videos/{videoID}/status", videoHandler.GetStatus)

		// Export operations (USB export only - download removed)
		if exportHandler != nil {
			r.Get("/export/estimate", exportHandler.Estimate)
			r.Post("/export/start", exportHandler.Start)
			r.Get("/export/status", exportHandler.Status)
			r.Post("/export/cancel", exportHandler.Cancel)
		}

		// USB drive operations (when USB Manager is available)
		if usbHandler != nil {
			r.Get("/usb/drives", usbHandler.ListDrives)
			r.Get("/usb/drives/events", usbHandler.DriveEvents)
			r.Post("/usb/drives/{device}/mount", usbHandler.MountDrive)
			r.Post("/usb/drives/{device}/unmount", usbHandler.UnmountDrive)
			r.Post("/usb/drives/{device}/format", usbHandler.FormatDrive)
			r.Post("/usb/drives/{device}/format/async", usbHandler.FormatDriveAsync)
			r.Post("/usb/drives/{device}/rename", usbHandler.RenameDrive)
			r.Get("/usb/format/{operationID}", usbHandler.FormatProgress)
			r.Get("/usb/health", usbHandler.Health)
		}

		// Event/Activity log operations
		if eventHandler != nil {
			r.Get("/events", eventHandler.List)
			r.Get("/events/recent", eventHandler.Recent)
			r.Get("/events/stats", eventHandler.Stats)
			r.Get("/events/stream", eventHandler.Stream)
			r.Get("/events/categories", eventHandler.Categories)
			r.Get("/events/severities", eventHandler.Severities)
		}

		// Extension credential sync (browser GraphQL passthrough)
		if extensionHandler != nil {
			r.Post("/extension/credentials", extensionHandler.SyncCredentials)
			r.Get("/extension/credentials/status", extensionHandler.CredentialsStatus)
			r.Post("/extension/credentials/clear", extensionHandler.ClearCredentials)
			r.Get("/extension/test-user-lookup", extensionHandler.TestUserLookup) // Debug endpoint
		}
	})

	return r
}
