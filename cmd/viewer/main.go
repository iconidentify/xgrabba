package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/iconidentify/xgrabba/pkg/crypto"
	"golang.org/x/term"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

// DecryptedArchive holds decrypted content in memory
type DecryptedArchive struct {
	mu       sync.RWMutex
	data     map[string][]byte // path -> decrypted content
	manifest map[string]string // original path -> encrypted filename
}

func main() {
	// Find the directory containing the executable
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding executable path: %v\n", err)
		os.Exit(1)
	}
	archiveDir := filepath.Dir(execPath)

	// Check if this is an encrypted archive
	dataEncPath := filepath.Join(archiveDir, "data.enc")
	isEncrypted := fileExists(dataEncPath)

	var decryptedArchive *DecryptedArchive
	var handler http.Handler

	if isEncrypted {
		// Prompt for password and decrypt
		fmt.Printf("XGrabba Archive Viewer %s\n", Version)
		fmt.Printf("Archive: %s\n\n", archiveDir)
		fmt.Println("This archive is encrypted.")

		password, err := promptPassword()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading password: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("\nDecrypting archive...")
		decryptedArchive, err = decryptArchive(archiveDir, password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
			fmt.Fprintf(os.Stderr, "Please check your password and try again.\n")
			os.Exit(1)
		}

		fmt.Printf("Decrypted %d files.\n\n", len(decryptedArchive.data))
		handler = createDecryptedHandler(archiveDir, decryptedArchive)
	} else {
		// Verify index.html exists for non-encrypted archives
		indexPath := filepath.Join(archiveDir, "index.html")
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: index.html not found in %s\n", archiveDir)
			fmt.Fprintf(os.Stderr, "Please run this viewer from the archive directory.\n")
			os.Exit(1)
		}
		handler = http.FileServer(http.Dir(archiveDir))
	}

	// Find an available port
	port, err := findAvailablePort()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding available port: %v\n", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://%s/", addr)

	// Setup HTTP server
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Start server
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Give server a moment to start
	time.Sleep(100 * time.Millisecond)

	// Check if server started successfully
	select {
	case err := <-serverErr:
		fmt.Fprintf(os.Stderr, "Error starting server: %v\n", err)
		os.Exit(1)
	default:
	}

	if !isEncrypted {
		fmt.Printf("XGrabba Archive Viewer %s\n", Version)
	}
	fmt.Printf("Serving archive from: %s\n", archiveDir)
	fmt.Printf("Server running at: %s\n", url)
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println()

	// Open browser
	if err := openBrowser(url); err != nil {
		fmt.Printf("Could not open browser automatically.\n")
		fmt.Printf("Please open %s in your browser.\n", url)
	}

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\nShutting down...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error during shutdown: %v\n", err)
	}

	fmt.Println("Goodbye!")
}

// promptPassword prompts for password without echoing
func promptPassword() (string, error) {
	fmt.Print("Enter password: ")

	// Try to read password without echo
	if term.IsTerminal(int(syscall.Stdin)) {
		password, err := term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			return "", err
		}
		fmt.Println()
		return string(password), nil
	}

	// Fallback for non-terminal input
	reader := bufio.NewReader(os.Stdin)
	password, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(password), nil
}

// decryptArchive decrypts the archive and loads content into memory
func decryptArchive(archiveDir, password string) (*DecryptedArchive, error) {
	archive := &DecryptedArchive{
		data:     make(map[string][]byte),
		manifest: make(map[string]string),
	}

	// 1. Decrypt data.enc (tweets data)
	dataEncPath := filepath.Join(archiveDir, "data.enc")
	encData, err := os.ReadFile(dataEncPath)
	if err != nil {
		return nil, fmt.Errorf("read data.enc: %w", err)
	}

	tweetsData, err := crypto.Decrypt(encData, password)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed - wrong password or corrupted data")
	}

	archive.data["tweets-data.json"] = tweetsData

	// 2. Decrypt manifest.enc to get file mappings
	manifestPath := filepath.Join(archiveDir, "manifest.enc")
	if fileExists(manifestPath) {
		encManifest, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("read manifest.enc: %w", err)
		}

		manifestData, err := crypto.Decrypt(encManifest, password)
		if err != nil {
			return nil, fmt.Errorf("decrypt manifest: %w", err)
		}

		if err := json.Unmarshal(manifestData, &archive.manifest); err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
	}

	// 3. Decrypt media files
	encryptedDir := filepath.Join(archiveDir, "encrypted")
	for originalPath, encFileName := range archive.manifest {
		encFilePath := filepath.Join(encryptedDir, encFileName)
		if !fileExists(encFilePath) {
			continue
		}

		encData, err := os.ReadFile(encFilePath)
		if err != nil {
			continue
		}

		decData, err := crypto.Decrypt(encData, password)
		if err != nil {
			continue
		}

		archive.data[originalPath] = decData
	}

	return archive, nil
}

// createDecryptedHandler creates an HTTP handler that serves decrypted content
func createDecryptedHandler(archiveDir string, archive *DecryptedArchive) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		path = strings.TrimPrefix(path, "/")

		// Check if this is a decrypted file
		archive.mu.RLock()
		content, exists := archive.data[path]
		archive.mu.RUnlock()

		if exists {
			// Serve decrypted content
			setContentType(w, path)
			w.WriteHeader(http.StatusOK)
			w.Write(content)
			return
		}

		// Check if it's a regular file on disk (index.html, viewer binaries)
		filePath := filepath.Join(archiveDir, path)
		if fileExists(filePath) {
			http.ServeFile(w, r, filePath)
			return
		}

		// Not found
		http.NotFound(w, r)
	})
}

// setContentType sets the appropriate Content-Type header based on file extension
func setContentType(w http.ResponseWriter, path string) {
	ext := strings.ToLower(filepath.Ext(path))
	contentTypes := map[string]string{
		".json": "application/json",
		".js":   "application/javascript",
		".html": "text/html",
		".css":  "text/css",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".mp4":  "video/mp4",
		".webm": "video/webm",
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".svg":  "image/svg+xml",
		".ico":  "image/x-icon",
	}

	if ct, ok := contentTypes[ext]; ok {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// findAvailablePort finds an available TCP port
func findAvailablePort() (int, error) {
	// Try to get a random available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

// openBrowser opens the default browser to the given URL
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}
