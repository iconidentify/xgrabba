package main

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/iconidentify/xgrabba/pkg/crypto"
	"github.com/iconidentify/xgrabba/pkg/ui"
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

type ManifestEntry struct {
	EncryptedName string `json:"enc_name"`
	OriginalSize  int64  `json:"original_size"`
	ChunkCount    int    `json:"chunk_count"`
	ContentType   string `json:"content_type"`
}

type ManifestFile struct {
	Version   int                      `json:"version"`
	ChunkSize int                      `json:"chunk_size"`
	Entries   map[string]ManifestEntry `json:"entries"`
}

type EncryptedArchive struct {
	archiveDir string
	data       map[string][]byte
	manifest   map[string]ManifestEntry
	key        []byte
	cache      *chunkCache
	offlineUI  []byte
}

type chunkKey struct {
	Path  string
	Index int
}

type chunkCache struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	items    map[chunkKey]*list.Element
}

type chunkCacheEntry struct {
	key  chunkKey
	data []byte
}

func newChunkCache(capacity int) *chunkCache {
	if capacity <= 0 {
		return nil
	}
	return &chunkCache{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[chunkKey]*list.Element),
	}
}

func (c *chunkCache) Get(key chunkKey) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*chunkCacheEntry).data, true
	}
	return nil, false
}

func (c *chunkCache) Add(key chunkKey, data []byte) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*chunkCacheEntry).data = data
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&chunkCacheEntry{key: key, data: data})
	c.items[key] = el
	if c.ll.Len() > c.capacity {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*chunkCacheEntry).key)
		}
	}
}

type DecryptedFile struct {
	file       *os.File
	key        []byte
	header     crypto.V2Header
	size       int64
	chunkCount int
	path       string
	cache      *chunkCache
}

func (d *DecryptedFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= d.size {
		return 0, io.EOF
	}
	maxRead := int64(len(p))
	remaining := d.size - off
	if remaining < maxRead {
		maxRead = remaining
	}
	if maxRead <= 0 {
		return 0, io.EOF
	}
	p = p[:maxRead]

	chunkSize := int64(d.header.ChunkSize)
	if chunkSize == 0 {
		return 0, fmt.Errorf("invalid chunk size")
	}
	if d.chunkCount == 0 {
		d.chunkCount = int((d.size + chunkSize - 1) / chunkSize)
	}

	startChunk := int(off / chunkSize)
	endChunk := int((off + maxRead - 1) / chunkSize)
	n := 0

	for chunkIdx := startChunk; chunkIdx <= endChunk; chunkIdx++ {
		chunkStart := int64(chunkIdx) * chunkSize
		chunkLen := chunkSize
		if chunkIdx == d.chunkCount-1 {
			chunkLen = d.size - chunkStart
		}
		if chunkLen < 0 {
			return n, io.EOF
		}

		key := chunkKey{Path: d.path, Index: chunkIdx}
		var chunk []byte
		if cached, ok := d.cache.Get(key); ok {
			chunk = cached
		} else {
			plain, err := crypto.DecryptChunkAt(d.file, d.key, d.header, chunkIdx, int(chunkLen))
			if err != nil {
				return n, err
			}
			chunk = plain
			d.cache.Add(key, chunk)
		}

		readStart := maxInt64(off, chunkStart)
		readEnd := minInt64(off+maxRead, chunkStart+int64(len(chunk)))
		if readEnd <= readStart {
			continue
		}
		copy(p[n:], chunk[readStart-chunkStart:readEnd-chunkStart])
		n += int(readEnd - readStart)
	}

	if int64(n) < maxRead {
		return n, io.EOF
	}
	return n, nil
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
		legacyArchive, encryptedArchive, err := decryptArchive(archiveDir, password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
			fmt.Fprintf(os.Stderr, "Please check your password and try again.\n")
			os.Exit(1)
		}

		if encryptedArchive != nil {
			fmt.Printf("Decrypted manifest. Serving files on-demand.\n\n")
			handler = createEncryptedHandler(archiveDir, encryptedArchive)
		} else {
			fmt.Printf("Decrypted %d files.\n\n", len(legacyArchive.data))
			handler = createDecryptedHandler(archiveDir, legacyArchive)
		}
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
func decryptArchive(archiveDir, password string) (*DecryptedArchive, *EncryptedArchive, error) {
	legacyArchive := &DecryptedArchive{
		data:     make(map[string][]byte),
		manifest: make(map[string]string),
	}

	// 1. Decrypt data.enc (tweets data)
	dataEncPath := filepath.Join(archiveDir, "data.enc")
	encData, err := os.ReadFile(dataEncPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read data.enc: %w", err)
	}

	tweetsData, err := crypto.Decrypt(encData, password)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt failed - wrong password or corrupted data")
	}

	legacyArchive.data["tweets-data.json"] = tweetsData

	// 2. Decrypt manifest.enc to get file mappings
	manifestPath := filepath.Join(archiveDir, "manifest.enc")
	if fileExists(manifestPath) {
		encManifest, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read manifest.enc: %w", err)
		}

		manifestData, err := crypto.Decrypt(encManifest, password)
		if err != nil {
			return nil, nil, fmt.Errorf("decrypt manifest: %w", err)
		}

		var manifestV2 ManifestFile
		if err := json.Unmarshal(manifestData, &manifestV2); err == nil && (manifestV2.Version >= 2 || len(manifestV2.Entries) > 0) {
			encryptedArchive := &EncryptedArchive{
				archiveDir: archiveDir,
				data:       map[string][]byte{"tweets-data.json": tweetsData},
				manifest:   manifestV2.Entries,
				cache:      newChunkCache(getChunkCacheSize()),
				offlineUI:  generateOfflineUI(tweetsData),
			}
			if len(manifestV2.Entries) > 0 {
				key, err := deriveKeyFromEncryptedFile(archiveDir, manifestV2.Entries, password)
				if err != nil {
					return nil, nil, err
				}
				encryptedArchive.key = key
			}
			return nil, encryptedArchive, nil
		}

		if err := json.Unmarshal(manifestData, &legacyArchive.manifest); err != nil {
			return nil, nil, fmt.Errorf("parse manifest: %w", err)
		}
		if len(legacyArchive.manifest) > 0 {
			fmt.Println("Legacy manifest detected; decrypting all files into memory (this may be slow).")
		}
	}

	// 3. Decrypt media files
	encryptedDir := filepath.Join(archiveDir, "encrypted")
	for originalPath, encFileName := range legacyArchive.manifest {
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

		legacyArchive.data[originalPath] = decData
	}

	return legacyArchive, nil, nil
}

// createDecryptedHandler creates an HTTP handler that serves decrypted content
func createDecryptedHandler(archiveDir string, archive *DecryptedArchive) http.Handler {
	// Generate offline UI with embedded data once at creation time
	var offlineUI []byte
	if tweetsData, ok := archive.data["tweets-data.json"]; ok {
		offlineUI = generateOfflineUI(tweetsData)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		path = strings.TrimPrefix(path, "/")

		// Serve the shared UI with injected data for index.html
		if path == "index.html" && len(offlineUI) > 0 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(w, r, "index.html", time.Now(), bytes.NewReader(offlineUI))
			return
		}

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

		// Check if it's a regular file on disk (viewer binaries, etc)
		filePath := filepath.Join(archiveDir, path)
		if fileExists(filePath) {
			http.ServeFile(w, r, filePath)
			return
		}

		// Not found
		http.NotFound(w, r)
	})
}

func createEncryptedHandler(archiveDir string, archive *EncryptedArchive) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		path = strings.TrimPrefix(path, "/")

		if path == "index.html" {
			w.Header().Set("Content-Type", "text/html")
			http.ServeContent(w, r, "index.html", time.Now(), bytes.NewReader(archive.offlineUI))
			return
		}

		if content, ok := archive.data[path]; ok {
			setContentType(w, path)
			http.ServeContent(w, r, filepath.Base(path), time.Now(), bytes.NewReader(content))
			return
		}

		if entry, ok := archive.manifest[path]; ok {
			encFilePath := filepath.Join(archiveDir, "encrypted", entry.EncryptedName)
			if !fileExists(encFilePath) {
				http.NotFound(w, r)
				return
			}
			f, err := os.Open(encFilePath)
			if err != nil {
				http.Error(w, "Failed to open encrypted file", http.StatusInternalServerError)
				return
			}
			defer f.Close()

			if archive.key == nil {
				http.Error(w, "Archive key unavailable", http.StatusInternalServerError)
				return
			}

			header, err := crypto.ReadV2HeaderAt(f)
			if err != nil {
				http.Error(w, "Failed to read encrypted header", http.StatusInternalServerError)
				return
			}

			reader := &DecryptedFile{
				file:       f,
				key:        archive.key,
				header:     header,
				size:       entry.OriginalSize,
				chunkCount: entry.ChunkCount,
				path:       path,
				cache:      archive.cache,
			}
			if entry.ContentType != "" {
				w.Header().Set("Content-Type", entry.ContentType)
			} else {
				setContentType(w, path)
			}
			http.ServeContent(w, r, filepath.Base(path), time.Now(), io.NewSectionReader(reader, 0, entry.OriginalSize))
			return
		}

		filePath := filepath.Join(archiveDir, path)
		if fileExists(filePath) {
			http.ServeFile(w, r, filePath)
			return
		}

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

func deriveKeyFromEncryptedFile(archiveDir string, entries map[string]ManifestEntry, password string) ([]byte, error) {
	for _, entry := range entries {
		encFilePath := filepath.Join(archiveDir, "encrypted", entry.EncryptedName)
		f, err := os.Open(encFilePath)
		if err != nil {
			continue
		}
		header, err := crypto.ReadV2HeaderAt(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		return crypto.DeriveKey(password, header.Salt), nil
	}
	return nil, fmt.Errorf("no encrypted files found to derive key")
}

func getChunkCacheSize() int {
	val := strings.TrimSpace(os.Getenv("XGRABBA_VIEWER_CHUNK_CACHE"))
	if val == "" {
		return 64
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 0 {
		return 64
	}
	return n
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// generateOfflineUI creates the offline viewer UI by injecting tweet data into the shared UI.
// This reuses the same index.html from the main app, ensuring consistent UI/UX.
// The shared UI has built-in OFFLINE_MODE detection via window.OFFLINE_DATA.
func generateOfflineUI(tweetsData []byte) []byte {
	// Parse the tweets data to create the OFFLINE_DATA structure
	var rawData struct {
		Tweets []json.RawMessage `json:"tweets"`
	}
	if err := json.Unmarshal(tweetsData, &rawData); err != nil {
		// Fallback: use empty tweets array
		rawData.Tweets = []json.RawMessage{}
	}

	// Create the data injection script
	// The shared UI looks for window.OFFLINE_DATA = { tweets: [...] }
	dataScript := fmt.Sprintf(`<script>
window.OFFLINE_DATA = %s;
</script>
</head>`, string(tweetsData))

	// Inject the data script right before </head> in the shared UI
	htmlStr := string(ui.IndexHTML)
	result := strings.Replace(htmlStr, "</head>", dataScript, 1)

	return []byte(result)
}

