package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iconidentify/xgrabba/internal/api"
	"github.com/iconidentify/xgrabba/internal/api/handler"
	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/downloader"
	"github.com/iconidentify/xgrabba/internal/repository"
	"github.com/iconidentify/xgrabba/internal/service"
	"github.com/iconidentify/xgrabba/internal/worker"
	"github.com/iconidentify/xgrabba/pkg/grok"
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
		dl,
		cfg.Storage,
		logger,
	)

	// Start AI metadata backfill in background for legacy tweets
	backfillCtx, cancelBackfill := context.WithCancel(context.Background())
	go tweetSvc.BackfillAIMetadata(backfillCtx)

	// Initialize handlers
	videoHandler := handler.NewVideoHandler(videoSvc, logger)
	tweetHandler := handler.NewTweetHandler(tweetSvc, logger)
	healthHandler := handler.NewHealthHandler(jobRepo)
	uiHandler := handler.NewUIHandler()

	// Setup router
	router := api.NewRouter(videoHandler, tweetHandler, healthHandler, uiHandler, cfg.Server.APIKey)

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

	// Cancel background tasks
	cancelBackfill()

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
