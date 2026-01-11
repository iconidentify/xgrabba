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
	apiKey string,
) *chi.Mux {
	r := chi.NewRouter()

	// Global middleware
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
	r.Get("/", uiHandler.Smart)       // Auto-detect mobile vs desktop
	r.Get("/ui", uiHandler.Index)     // Full archive browser
	r.Get("/quick", uiHandler.Quick)  // Mobile-optimized quick archive
	r.Get("/q", uiHandler.Quick)      // Short alias for mobile

	// API v1 (authenticated)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(mw.APIKeyAuth(apiKey))

		// Tweet operations (new - full tweet archival)
		r.Post("/tweets", tweetHandler.Archive)
		r.Get("/tweets", tweetHandler.List)
		r.Get("/tweets/{tweetID}", tweetHandler.Get)
		r.Get("/tweets/{tweetID}/status", tweetHandler.GetStatus)
		r.Delete("/tweets/{tweetID}", tweetHandler.Delete)

		// Video operations (legacy - kept for backwards compatibility)
		r.Post("/videos", videoHandler.Submit)
		r.Get("/videos", videoHandler.List)
		r.Get("/videos/{videoID}", videoHandler.Get)
		r.Get("/videos/{videoID}/status", videoHandler.GetStatus)
	})

	return r
}
