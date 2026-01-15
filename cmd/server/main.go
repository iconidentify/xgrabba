package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/iconidentify/xgrabba/internal/api"
	"github.com/iconidentify/xgrabba/internal/api/handler"
	"github.com/iconidentify/xgrabba/internal/bookmarks"
	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/downloader"
	"github.com/iconidentify/xgrabba/internal/repository"
	"github.com/iconidentify/xgrabba/internal/service"
	"github.com/iconidentify/xgrabba/internal/worker"
	"github.com/iconidentify/xgrabba/pkg/grok"
	"github.com/iconidentify/xgrabba/pkg/twitter"
	"github.com/iconidentify/xgrabba/pkg/usbclient"
	"github.com/iconidentify/xgrabba/pkg/whisper"
	_ "github.com/mattn/go-sqlite3"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	// Parse flags
	configPath := flag.String("config", "", "Path to config file")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("xgrabba %s (built %s)\n", Version, BuildTime)
		os.Exit(0)
	}

	// Setup logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting xgrabba",
		"version", Version,
		"build_time", BuildTime,
	)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Ensure storage directories exist
	if err := os.MkdirAll(cfg.Storage.BasePath, 0755); err != nil {
		logger.Error("failed to create storage directory", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.Storage.TempPath, 0755); err != nil {
		logger.Error("failed to create temp directory", "error", err)
		os.Exit(1)
	}

	// Initialize dependencies
	videoRepo := repository.NewFilesystemVideoRepository(cfg.Storage)
	jobRepo := repository.NewInMemoryJobRepository()
	grokClient := grok.NewClient(cfg.Grok)
	dl := downloader.NewHTTPDownloader(cfg.Download)

	// Initialize Whisper client for audio transcription
	var whisperClient *whisper.HTTPClient
	if cfg.Whisper.Enabled && cfg.Whisper.APIKey != "" {
		whisperClient = whisper.NewClient(whisper.Config{
			APIKey:  cfg.Whisper.APIKey,
			BaseURL: cfg.Whisper.BaseURL,
			Model:   cfg.Whisper.Model,
			Timeout: cfg.Whisper.Timeout,
		})
		logger.Info("whisper transcription enabled", "model", cfg.Whisper.Model)
	} else {
		logger.Info("whisper transcription disabled (no API key or disabled)")
	}

	// Event service for activity log / admin console (created early so other services can emit events)
	// Enable SQLite persistence so events survive pod restarts
	eventsDBPath := filepath.Join(cfg.Storage.BasePath, ".events.db")
	eventSvc, err := service.NewEventService(service.EventServiceConfig{
		RingBufferSize:  1000,
		PersistToSQLite: true,
		SQLitePath:      eventsDBPath,
		RetentionDays:   90, // Keep events for 90 days
	}, logger)
	if err != nil {
		logger.Error("failed to create event service", "error", err)
		os.Exit(1)
	}
	defer eventSvc.Close()
	logger.Info("event service initialized with SQLite persistence", "db_path", eventsDBPath)

	// Initialize services
	videoSvc := service.NewVideoService(
		videoRepo,
		jobRepo,
		grokClient,
		dl,
		cfg.Storage,
		cfg.Worker,
		logger,
	)

	// Initialize tweet service (new architecture - backend handles everything)
	tweetSvc := service.NewTweetService(
		grokClient,
		whisperClient,
		dl,
		cfg.Storage,
		cfg.AI,
		cfg.Whisper.Enabled,
		logger,
		eventSvc,
	)

	// Initialize export service with storage path for state persistence
	exportSvc := service.NewExportService(tweetSvc, logger, eventSvc, cfg.Storage.BasePath)

	// Start AI metadata backfill in background for legacy tweets
	backfillCtx, cancelBackfill := context.WithCancel(context.Background())
	go tweetSvc.BackfillAIMetadata(backfillCtx)

	// Recover orphaned archives (directories with temp_processing but no tweet.json)
	go tweetSvc.RecoverOrphanedArchives(context.Background())

	// Resume incomplete archives (tweets saved mid-processing before restart)
	go tweetSvc.ResumeIncompleteArchives(context.Background())

	// Twitter client (shared). Used for extension credential storage and optional GraphQL access.
	twitterClient := twitter.NewClient(logger)

	// Start bookmarks monitor (optional) to auto-archive newly bookmarked tweets (mobile-friendly).
	bookmarksCtx, cancelBookmarks := context.WithCancel(context.Background())
	var bookmarksOAuthHandler *handler.BookmarksOAuthHandler // Declared here so watching goroutine can access it

	// Bookmarks handler provides status + monitor control endpoints.
	// OAuth connect routes will return an error if TWITTER_OAUTH_CLIENT_ID isn't configured.
	if cfg.Bookmarks.Enabled || cfg.Bookmarks.OAuthClientID != "" {
		bookmarksOAuthHandler = handler.NewBookmarksOAuthHandler(cfg.Bookmarks, cfg.Server.APIKey, logger, twitterClient)
	}

	if cfg.Bookmarks.Enabled {
		if cfg.Bookmarks.UseBrowserCredentials {
			logger.Info("bookmarks auth: browser credentials (GraphQL) enabled")
			gqlClient := twitter.NewGraphQLBookmarksClient(twitterClient)
			mon := bookmarks.NewMonitor(cfg.Bookmarks, gqlClient, tweetSvc, logger)
			mon.SetEventEmitter(eventSvc)
			if bookmarksOAuthHandler != nil {
				bookmarksOAuthHandler.SetMonitor(mon)
			}
			go mon.Start(bookmarksCtx)
			goto handlers
		}

		ua := "xgrabba-bookmarks-monitor/" + Version

		// Allow "configured forever" mode: if refresh token / user id are not provided via env/secret,
		// load them from the on-disk OAuth store written by the connect flow.
		var stored *bookmarks.OAuthStore
		if cfg.Bookmarks.OAuthStorePath != "" {
			if s, err := bookmarks.LoadOAuthStore(cfg.Bookmarks.OAuthStorePath); err == nil {
				stored = s
			} else {
				logger.Info("bookmarks oauth store not present yet", "path", cfg.Bookmarks.OAuthStorePath)
			}
		}

		bmCfg := cfg.Bookmarks
		if bmCfg.UserID == "" && stored != nil && stored.UserID != "" {
			bmCfg.UserID = stored.UserID
		}
		refreshToken := bmCfg.RefreshToken
		if refreshToken == "" && stored != nil && stored.RefreshToken != "" {
			refreshToken = stored.RefreshToken
		}

		var tokens twitter.TokenSource
		if refreshToken != "" && bmCfg.OAuthClientID != "" {
			tokens = twitter.NewOAuth2RefreshTokenSource(twitter.OAuth2RefreshTokenSourceConfig{
				TokenURL:      bmCfg.TokenURL,
				ClientID:      bmCfg.OAuthClientID,
				ClientSecret:  bmCfg.OAuthClientSecret,
				RefreshToken:  refreshToken,
				HTTPTimeout:   15 * time.Second,
				UserAgent:     ua,
				RefreshSkew:   30 * time.Second,
				OnRefreshToken: func(newRT string) {
					if bmCfg.OAuthStorePath == "" || bmCfg.UserID == "" {
						return
					}
					_ = bookmarks.SaveOAuthStore(bmCfg.OAuthStorePath, bookmarks.OAuthStore{
						UserID:       bmCfg.UserID,
						RefreshToken: newRT,
					})
				},
			})
			logger.Info("bookmarks auth: oauth2 refresh token enabled")
		} else if bmCfg.BearerToken != "" {
			tokens = &twitter.StaticTokenSource{TokenValue: bmCfg.BearerToken}
			logger.Info("bookmarks auth: static bearer token enabled")
		} else {
			// Enabled but not connected yet; user must complete connect flow. We'll start watching
			// for the on-disk OAuth store and automatically start the monitor once it appears.
			logger.Warn("bookmarks enabled but no token available yet; complete /bookmarks/oauth/start flow", "store_path", bmCfg.OAuthStorePath)
			go func() {
				ticker := time.NewTicker(10 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-bookmarksCtx.Done():
						return
					case <-ticker.C:
						if bmCfg.OAuthStorePath == "" || bmCfg.OAuthClientID == "" {
							return
						}
						st, err := bookmarks.LoadOAuthStore(bmCfg.OAuthStorePath)
						if err != nil || st == nil || st.UserID == "" || st.RefreshToken == "" {
							continue
						}
						logger.Info("bookmarks oauth store detected; starting monitor", "user_id", st.UserID)

						rtTokens := twitter.NewOAuth2RefreshTokenSource(twitter.OAuth2RefreshTokenSourceConfig{
							TokenURL:     bmCfg.TokenURL,
							ClientID:     bmCfg.OAuthClientID,
							ClientSecret: bmCfg.OAuthClientSecret,
							RefreshToken: st.RefreshToken,
							HTTPTimeout:  15 * time.Second,
							UserAgent:    ua,
							RefreshSkew:  30 * time.Second,
							OnRefreshToken: func(newRT string) {
								_ = bookmarks.SaveOAuthStore(bmCfg.OAuthStorePath, bookmarks.OAuthStore{
									UserID:       st.UserID,
									RefreshToken: newRT,
								})
							},
						})

						rtClient := twitter.NewBookmarksClient(twitter.BookmarksClientConfig{
							BaseURL:   bmCfg.BaseURL,
							Tokens:    rtTokens,
							Timeout:   15 * time.Second,
							UserAgent: ua,
						})
						startCfg := bmCfg
						startCfg.UserID = st.UserID
						mon := bookmarks.NewMonitor(startCfg, rtClient, tweetSvc, logger)
						mon.SetEventEmitter(eventSvc)
						// Register monitor with handler for control endpoints
						// (handler is already created by the time this goroutine detects the OAuth store)
						if bookmarksOAuthHandler != nil {
							bookmarksOAuthHandler.SetMonitor(mon)
						}
						go mon.Start(bookmarksCtx)
						return
					}
				}
			}()
			tokens = nil
		}

		if tokens != nil && bmCfg.UserID != "" {
			bmClient := twitter.NewBookmarksClient(twitter.BookmarksClientConfig{
				BaseURL:   bmCfg.BaseURL,
				Tokens:    tokens,
				Timeout:   15 * time.Second,
				UserAgent: ua,
			})
			mon := bookmarks.NewMonitor(bmCfg, bmClient, tweetSvc, logger)
			mon.SetEventEmitter(eventSvc)
			if bookmarksOAuthHandler != nil {
				bookmarksOAuthHandler.SetMonitor(mon)
			}
			go mon.Start(bookmarksCtx)
		}
	}

handlers:
	// Initialize handlers
	videoHandler := handler.NewVideoHandler(videoSvc, logger)
	tweetHandler := handler.NewTweetHandler(tweetSvc, logger)
	healthHandler := handler.NewHealthHandler(jobRepo)
	uiHandler := handler.NewUIHandler()
	exportHandler := handler.NewExportHandler(exportSvc, logger)

	// USB handler (optional). Enables USB drive export when USB Manager is running.
	var usbHandler *handler.USBHandler
	if cfg.USB.Enabled {
		usbClient := usbclient.NewClient(cfg.USB.ManagerURL, cfg.Server.APIKey)
		usbHandler = handler.NewUSBHandler(usbClient, logger)
		logger.Info("USB export enabled", "manager_url", cfg.USB.ManagerURL)
	}

	eventHandler := handler.NewEventHandler(eventSvc, logger)

	// Extension handler (browser GraphQL passthrough)
	extensionHandler := handler.NewExtensionHandler(twitterClient)

	// Setup router
	router := api.NewRouter(videoHandler, tweetHandler, healthHandler, uiHandler, exportHandler, bookmarksOAuthHandler, usbHandler, eventHandler, extensionHandler, cfg.Server.APIKey)

	// Initialize worker pool
	pool := worker.NewPool(
		worker.Config{
			Workers:      cfg.Worker.Count,
			PollInterval: cfg.Worker.PollInterval,
		},
		jobRepo,
		videoSvc,
		logger,
	)

	// Start worker pool
	pool.Start()

	// Setup HTTP server
	srv := &http.Server{
		Addr:         cfg.Server.Address(),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start server in goroutine
	go func() {
		logger.Info("starting HTTP server", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down")

	// Save export state before shutdown
	exportSvc.SaveExportStateOnShutdown()
	logger.Info("export state saved")

	// Cancel background tasks
	cancelBackfill()
	cancelBookmarks()

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop accepting new requests
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	// Stop workers (allow in-flight jobs to complete)
	if err := pool.Stop(25 * time.Second); err != nil {
		logger.Error("worker pool shutdown error", "error", err)
	}

	logger.Info("shutdown complete")
}
