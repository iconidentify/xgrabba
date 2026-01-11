# XGrabba

X.com Video Archive Tool - Archive videos from X.com with AI-powered intelligent naming.

## Features

- **One-click archiving**: Archive any video from X.com with a single click
- **AI-powered naming**: Uses Grok AI to generate descriptive, intelligent filenames
- **Full metadata**: Stores tweet text, author info, timestamps alongside videos
- **SMB access**: Browse archived videos easily via network share
- **Kubernetes-ready**: Deploy anywhere with Helm charts

## Architecture

```
Browser Extension  -->  Go Backend Server  -->  Filesystem/SMB
     |                        |
     |                        v
     |                   Grok AI (naming)
     v
 X.com (video URLs)
```

## Quick Start

### Prerequisites

- Go 1.22+
- Docker (for containerized deployment)
- Kubernetes + Helm (for cluster deployment)
- Chrome or Edge browser

### Local Development

1. **Clone the repository**
   ```bash
   git clone https://github.com/iconidentify/xgrabba.git
   cd xgrabba
   ```

2. **Configure environment**
   ```bash
   export API_KEY=your-secret-api-key
   export GROK_API_KEY=your-grok-api-key
   export STORAGE_PATH=/path/to/video/storage
   ```

3. **Run the server**
   ```bash
   make run
   ```

4. **Load the extension**
   - Open Chrome/Edge
   - Navigate to `chrome://extensions`
   - Enable "Developer mode"
   - Click "Load unpacked"
   - Select the `extension/` directory

5. **Configure the extension**
   - Click the XGrabba icon in your browser toolbar
   - Go to Settings
   - Enter your backend URL (default: `http://localhost:9847`)
   - Enter your API key

### Docker Deployment

```bash
# Build the image
docker build -t xgrabba:latest .

# Run the container
docker run -d \
  -p 8080:8080 \
  -e API_KEY=your-api-key \
  -e GROK_API_KEY=your-grok-api-key \
  -v /path/to/videos:/data/videos \
  xgrabba:latest
```

### Kubernetes Deployment

```bash
# Add secrets
kubectl create secret generic xgrabba-secrets \
  --from-literal=API_KEY=your-api-key \
  --from-literal=GROK_API_KEY=your-grok-api-key

# Install with Helm
helm install xgrabba deployments/helm/xgrabba \
  --set secrets.apiKey=your-api-key \
  --set secrets.grokApiKey=your-grok-api-key
```

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `API_KEY` | API key for extension authentication | *required* |
| `GROK_API_KEY` | Grok AI API key for filename generation | *required* |
| `SERVER_HOST` | Server bind address | `0.0.0.0` |
| `SERVER_PORT` | Server port | `8080` |
| `STORAGE_PATH` | Video storage directory | `/data/videos` |
| `STORAGE_TEMP_PATH` | Temporary file directory | `/data/temp` |
| `WORKER_COUNT` | Number of background workers | `2` |
| `GROK_MODEL` | Grok model to use | `grok-beta` |

### YAML Configuration

Create a `config.yaml` file:

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  api_key: "your-api-key"

storage:
  base_path: "/data/videos"
  temp_path: "/data/temp"

worker:
  count: 2
  poll_interval: "5s"
  max_retries: 3

grok:
  api_key: "your-grok-api-key"
  model: "grok-beta"
```

Run with config file:
```bash
./xgrabba --config config.yaml
```

## API Reference

### Submit Video

```http
POST /api/v1/videos
Content-Type: application/json
X-API-Key: your-api-key

{
  "tweet_url": "https://x.com/user/status/123456789",
  "media_urls": ["https://video.twimg.com/..."],
  "metadata": {
    "author_username": "username",
    "author_name": "Display Name",
    "tweet_text": "Tweet content...",
    "posted_at": "2024-01-15T10:30:00Z",
    "duration_seconds": 45
  }
}
```

Response:
```json
{
  "video_id": "vid_abc123",
  "status": "pending",
  "message": "Video queued for processing"
}
```

### Get Video Status

```http
GET /api/v1/videos/{videoID}/status
X-API-Key: your-api-key
```

Response:
```json
{
  "video_id": "vid_abc123",
  "status": "completed",
  "filename": "username_2024-01-15_description.mp4",
  "file_path": "/data/videos/2024/01/username_2024-01-15_description.mp4"
}
```

### List Videos

```http
GET /api/v1/videos?status=completed&limit=50
X-API-Key: your-api-key
```

### Health Check

```http
GET /health
GET /ready
```

## Storage Structure

Videos are organized by year and month:

```
/data/videos/
├── 2024/
│   ├── 01/
│   │   ├── username_2024-01-15_rocket-launch.mp4
│   │   └── username_2024-01-15_rocket-launch.json
│   └── 02/
│       └── ...
└── 2025/
    └── ...
```

Each video has a corresponding `.json` metadata file:

```json
{
  "video_id": "vid_abc123",
  "tweet_url": "https://x.com/elonmusk/status/123456789",
  "tweet_id": "123456789",
  "author_username": "elonmusk",
  "author_name": "Elon Musk",
  "tweet_text": "Check out this amazing launch!",
  "posted_at": "2024-01-15T10:30:00Z",
  "archived_at": "2024-01-15T12:45:00Z",
  "duration_seconds": 45,
  "resolution": "1280x720",
  "original_urls": ["https://video.twimg.com/..."],
  "generated_filename": "elonmusk_2024-01-15_rocket-launch.mp4",
  "grok_analysis": "SpaceX Starship test flight"
}
```

## Extension Usage

1. **Browse X.com** - Navigate to any tweet with a video
2. **Click archive button** - Look for the download icon in the tweet action bar
3. **Wait for confirmation** - The button will show a checkmark when complete
4. **View history** - Click the extension icon to see recent archives

### Keyboard Shortcut

Press `Alt+S` (or `Option+S` on Mac) to archive the video in the currently visible tweet.

## Development

### Build

```bash
make build          # Build binary
make test           # Run tests
make lint           # Run linter
make docker-build   # Build Docker image
```

### Project Structure

```
xgrabba/
├── cmd/server/         # Application entry point
├── internal/
│   ├── api/           # HTTP handlers and middleware
│   ├── config/        # Configuration management
│   ├── domain/        # Domain entities
│   ├── downloader/    # Video download logic
│   ├── repository/    # Data persistence
│   ├── service/       # Business logic
│   └── worker/        # Background job processing
├── pkg/grok/          # Grok AI client
├── extension/         # Chrome/Edge extension
└── deployments/helm/  # Kubernetes Helm charts
```

## SMB Share Setup

To browse archived videos over the network:

### Using Samba (Linux/macOS)

```bash
# Install Samba
sudo apt install samba  # Debian/Ubuntu
brew install samba      # macOS

# Add share configuration to /etc/samba/smb.conf
[xgrabba]
   path = /data/videos
   browseable = yes
   read only = yes
   guest ok = yes

# Restart Samba
sudo systemctl restart smbd
```

### Docker with Samba Sidecar

Use a Samba container alongside XGrabba sharing the same volume.

## License

MIT License - See [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests (`make test`)
5. Submit a pull request
