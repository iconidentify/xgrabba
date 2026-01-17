package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/internal/downloader"
	"github.com/iconidentify/xgrabba/internal/service"
	"github.com/iconidentify/xgrabba/pkg/grok"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	// Parse flags
	dest := flag.String("dest", "", "Destination path for export (required)")
	viewerDir := flag.String("viewers", "", "Directory containing viewer binaries to include")
	configPath := flag.String("config", "", "Path to config file")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("xgrabba-export %s (built %s)\n", Version, BuildTime)
		os.Exit(0)
	}

	if *dest == "" {
		fmt.Fprintln(os.Stderr, "Error: --dest flag is required")
		fmt.Fprintln(os.Stderr, "Usage: xgrabba-export --dest /path/to/usb")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("XGrabba Export",
		"version", Version,
		"dest", *dest,
	)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Verify storage path exists
	if _, err := os.Stat(cfg.Storage.BasePath); os.IsNotExist(err) {
		logger.Error("storage path does not exist", "path", cfg.Storage.BasePath)
		os.Exit(1)
	}

	// Initialize minimal dependencies for TweetService
	// We need TweetService to load existing tweets from disk
	grokClient := grok.NewClient(cfg.Grok)
	dl := downloader.NewHTTPDownloader(cfg.Download)

	tweetSvc := service.NewTweetService(
		grokClient,
		nil, // No whisper needed for export
		dl,
		cfg.Storage,
		cfg.AI,
		false, // Whisper disabled
		logger,
		nil, // No event emitter for CLI
	)

	// Create export service (no storage path for CLI, no persistence, no playlist service)
	exportSvc := service.NewExportService(tweetSvc, nil, logger, nil, "")

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nExport cancelled")
		cancel()
	}()

	// Run export
	opts := service.ExportOptions{
		DestPath:       *dest,
		IncludeViewers: *viewerDir != "",
		ViewerBinDir:   *viewerDir,
	}

	result, err := exportSvc.ExportToUSB(ctx, opts)
	if err != nil {
		if ctx.Err() != nil {
			logger.Info("export was cancelled")
			os.Exit(130) // Cancelled by signal
		}
		logger.Error("export failed", "error", err)
		os.Exit(1)
	}

	// Print summary
	fmt.Println()
	fmt.Println("Export Complete!")
	fmt.Println("----------------")
	fmt.Printf("Destination: %s\n", result.DestPath)
	fmt.Printf("Tweets: %d\n", result.TweetsCount)
	fmt.Printf("Media files: %d\n", result.MediaCount)
	fmt.Printf("Total size: %.2f MB\n", float64(result.TotalSize)/(1024*1024))
	fmt.Println()
	fmt.Println("To view the archive:")
	fmt.Println("  1. Open the destination folder")
	fmt.Println("  2. Run the appropriate viewer for your OS:")
	fmt.Println("     - Windows: xgrabba-viewer.exe")
	fmt.Println("     - macOS: xgrabba-viewer-mac")
	fmt.Println("     - Linux: xgrabba-viewer-linux")
	fmt.Println()
}
