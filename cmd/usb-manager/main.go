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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/iconidentify/xgrabba/pkg/usbmanager"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	// Parse flags
	showVersion := flag.Bool("version", false, "Show version and exit")
	port := flag.String("port", "8080", "HTTP server port")
	exportPath := flag.String("export-path", "/mnt/xgrabba-export", "Base path for USB exports")
	apiKey := flag.String("api-key", "", "API key for authentication (or set API_KEY env var)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("xgrabba-usb-manager %s (built %s)\n", Version, BuildTime)
		os.Exit(0)
	}

	// Setup logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting xgrabba-usb-manager",
		"version", Version,
		"build_time", BuildTime,
	)

	// Get API key from env if not set via flag
	key := *apiKey
	if key == "" {
		key = os.Getenv("API_KEY")
	}

	// Get export path from env if not set via flag
	basePath := *exportPath
	if envPath := os.Getenv("EXPORT_BASE_PATH"); envPath != "" {
		basePath = envPath
	}

	// Ensure export directory exists
	if err := os.MkdirAll(basePath, 0755); err != nil {
		logger.Error("failed to create export directory", "path", basePath, "error", err)
		os.Exit(1)
	}

	// Initialize USB manager
	manager := usbmanager.NewManager(basePath, logger)

	// Initial scan
	drives, err := manager.ScanDrives(context.Background())
	if err != nil {
		logger.Warn("initial drive scan failed", "error", err)
	} else {
		logger.Info("initial drive scan complete", "drives_found", len(drives))
	}

	// Initialize API handler
	apiHandler := usbmanager.NewAPIHandler(manager, key, logger)

	// Setup router
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Register routes
	r.Route("/api/v1/usb", func(r chi.Router) {
		apiHandler.RegisterRoutes(r)
	})

	// Health endpoint without auth
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// Start server
	addr := ":" + *port
	server := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // Long timeout for format operations
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.Info("HTTP server starting", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	logger.Info("shutdown complete")
}
