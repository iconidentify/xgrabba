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
				offlineUI:  []byte(generateOfflineHTML()),
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

// generateOfflineHTML generates a built-in offline viewer UI for encrypted archives.
// This avoids relying on the on-disk index.html, which is an encrypted notice page.
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
