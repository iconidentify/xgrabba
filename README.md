# XGrabba

X.com Tweet Archive Tool - Archive complete tweets including all media (images, videos, GIFs) with AI-powered intelligent naming.

## Features

- **Full tweet archiving**: Archive any tweet from X.com - text, images, videos, and GIFs
- **One-click from browser**: Simple Chrome/Edge extension with archive button on every tweet
- **AI-powered naming**: Uses Grok AI to generate descriptive, intelligent filenames
- **Complete metadata**: Stores tweet text, author info, metrics, timestamps in JSON and Markdown
- **Backend-driven**: Just send tweet URL - server handles all fetching and processing
- **Kubernetes-ready**: Deploy anywhere with Helm charts and Crossplane support
- **Auto-updates**: Crossplane automatically updates to new releases

## Architecture

```
Browser Extension  -->  Go Backend Server  -->  Filesystem Storage
     |                        |
     |                        v
     |               Twitter Syndication API
     |                        |
     |                        v
     v                   Grok AI (naming)
 Tweet URL only
```

The extension only sends the tweet URL. The backend:
1. Fetches tweet data from Twitter's syndication API
2. Generates an AI-powered filename using Grok
3. Downloads all media (images, videos, GIFs)
4. Saves metadata as JSON and human-readable Markdown

## Quick Start

### Prerequisites

- Go 1.22+ (for local development)
- Docker (for containerized deployment)
- Kubernetes + Helm (for cluster deployment)
- Chrome or Edge browser
- Grok API key from [x.ai](https://x.ai)

### Local Development

1. **Clone the repository**
   ```bash
   git clone https://github.com/iconidentify/xgrabba.git
   cd xgrabba
   ```

2. **Configure environment**
   ```bash
   cp .env.example .env
   # Edit .env with your values:
   # - API_KEY: secure key for extension auth
   # - GROK_API_KEY: your Grok API key
   # - STORAGE_PATH: where to store archived tweets
   ```

3. **Run the server**
   ```bash
   make run
   # Or manually:
   export $(cat .env | xargs) && go run ./cmd/server
   ```

4. **Load the extension**
   - Open Chrome/Edge and navigate to `chrome://extensions`
   - Enable "Developer mode"
   - Click "Load unpacked"
   - Select the `extension/` directory

5. **Configure the extension**
   - Click the XGrabba icon in your browser toolbar
   - Go to Settings
   - Enter your backend URL (default: `http://localhost:9847`)
   - Enter your API key

---

## Kubernetes Deployment

### Using Helm (Direct)

```bash
# Install directly from OCI registry
helm install xgrabba oci://ghcr.io/iconidentify/charts/xgrabba \
  --namespace xgrabba \
  --create-namespace \
  --set secrets.apiKey="your-secure-api-key" \
  --set secrets.grokApiKey="your-grok-api-key"
```

### Using Crossplane (Recommended)

Crossplane with provider-helm enables GitOps-style management with automatic updates.

#### Prerequisites

1. **Crossplane installed** in your cluster
2. **provider-helm** installed and configured:
   ```bash
   kubectl apply -f - <<EOF
   apiVersion: pkg.crossplane.io/v1
   kind: Provider
   metadata:
     name: provider-helm
   spec:
     package: xpkg.upbound.io/crossplane-contrib/provider-helm:v0.19.0
   EOF
   ```

3. **ProviderConfig** for Helm:
   ```bash
   kubectl apply -f - <<EOF
   apiVersion: helm.crossplane.io/v1beta1
   kind: ProviderConfig
   metadata:
     name: helm-provider
   spec:
     credentials:
       source: InjectedIdentity
   EOF
   ```

4. **GHCR credentials** for pulling Helm charts:
   ```bash
   kubectl create secret generic ghcr-helm-credentials \
     --namespace crossplane-system \
     --from-file=credentials=<(echo '{"auths":{"ghcr.io":{"auth":"'$(echo -n "USERNAME:GITHUB_TOKEN" | base64)'"}}}')
   ```

#### Deploy XGrabba

1. **Create namespace and secrets** (do this first)
   ```bash
   kubectl create namespace xgrabba

   # Image pull secret
   kubectl create secret docker-registry ghcr-secret \
     --namespace xgrabba \
     --docker-server=ghcr.io \
     --docker-username=USERNAME \
     --docker-password=GITHUB_TOKEN

   # App secrets
   kubectl create secret generic xgrabba-secrets \
     --namespace xgrabba \
     --from-literal=api-key="$(openssl rand -hex 32)" \
     --from-literal=grok-api-key="YOUR_GROK_API_KEY"
   ```

2. **Install with one command** (no repo clone needed)
   ```bash
   kubectl apply -f https://raw.githubusercontent.com/iconidentify/xgrabba/main/deployments/crossplane/install.yaml
   ```

3. **Check deployment status**
   ```bash
   kubectl get release xgrabba
   kubectl get pods -n xgrabba
   ```

See [docs/KUBERNETES-INSTALL.md](docs/KUBERNETES-INSTALL.md) for full installation guide including Samba setup and troubleshooting.

#### Auto-Updates

By default, Crossplane will automatically update when new chart versions are published. To pin to a specific version, download and edit the manifest:

```bash
# Download the manifest
curl -o xgrabba-release.yaml https://raw.githubusercontent.com/iconidentify/xgrabba/main/deployments/crossplane/install.yaml

# Edit to pin version
# Add under spec.forProvider.chart:
#   version: "0.1.0"

kubectl apply -f xgrabba-release.yaml
```

#### Customization

Download the manifest and edit to customize:

```yaml
spec:
  forProvider:
    values:
      # Scale up workers
      config:
        worker:
          count: 4

      # Configure ingress
      ingress:
        enabled: true
        className: "nginx"
        annotations:
          cert-manager.io/cluster-issuer: "letsencrypt-prod"
        hosts:
          - host: xgrabba.example.com
            paths:
              - path: /
                pathType: Prefix
        tls:
          - secretName: xgrabba-tls
            hosts:
              - xgrabba.example.com

      # Increase storage
      persistence:
        size: 500Gi
        storageClass: "fast-storage"

      # More resources
      resources:
        requests:
          cpu: 500m
          memory: 512Mi
        limits:
          cpu: 2000m
          memory: 2Gi
```

---

## Docker Deployment

```bash
# Pull from GHCR
docker pull ghcr.io/iconidentify/xgrabba:latest

# Run the container
docker run -d \
  --name xgrabba \
  -p 9847:9847 \
  -e API_KEY=your-secure-api-key \
  -e GROK_API_KEY=your-grok-api-key \
  -v /path/to/videos:/data/videos \
  ghcr.io/iconidentify/xgrabba:latest
```

---

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `API_KEY` | API key for extension authentication | *required* |
| `GROK_API_KEY` | Grok AI API key for filename generation | *required* |
| `SERVER_HOST` | Server bind address | `0.0.0.0` |
| `SERVER_PORT` | Server port | `9847` |
| `STORAGE_PATH` | Tweet storage directory | `/data/videos` |
| `STORAGE_TEMP_PATH` | Temporary file directory | `/data/temp` |
| `WORKER_COUNT` | Number of background workers | `2` |
| `GROK_MODEL` | Grok model to use | `grok-3` |

---

## API Reference

### Archive Tweet (New - Recommended)

```http
POST /api/v1/tweets
Content-Type: application/json
X-API-Key: your-api-key

{
  "tweet_url": "https://x.com/user/status/123456789"
}
```

Response:
```json
{
  "tweet_id": "123456789",
  "status": "pending",
  "message": "Tweet queued for archiving"
}
```

### Get Tweet Status

```http
GET /api/v1/tweets/{tweetID}
X-API-Key: your-api-key
```

Response:
```json
{
  "tweet_id": "123456789",
  "status": "completed",
  "author": "username",
  "text": "Tweet content...",
  "media_count": 2,
  "ai_title": "username_2024-01-15_rocket_launch",
  "archive_path": "/data/videos/2024/01/username_2024-01-15_123456789"
}
```

### List Archived Tweets

```http
GET /api/v1/tweets?limit=50&offset=0
X-API-Key: your-api-key
```

### Health Checks

```http
GET /health   # Liveness probe
GET /ready    # Readiness probe
```

---

## Storage Structure

Tweets are organized by year and month:

```
/data/videos/
├── 2024/
│   ├── 01/
│   │   └── username_2024-01-15_123456789/
│   │       ├── tweet.json       # Full metadata
│   │       ├── README.md        # Human-readable summary
│   │       └── media/
│   │           ├── photo_0.jpg
│   │           ├── photo_1.jpg
│   │           └── video_0.mp4
│   └── 02/
│       └── ...
└── 2025/
    └── ...
```

### Metadata JSON

```json
{
  "tweet_id": "123456789",
  "url": "https://x.com/username/status/123456789",
  "author": {
    "id": "987654321",
    "username": "username",
    "display_name": "Display Name",
    "verified": true
  },
  "text": "Check out this amazing content!",
  "posted_at": "2024-01-15T10:30:00Z",
  "archived_at": "2024-01-15T12:45:00Z",
  "media": [
    {
      "id": "photo_0",
      "type": "image",
      "url": "https://pbs.twimg.com/...",
      "local_path": "media/photo_0.jpg",
      "downloaded": true
    }
  ],
  "metrics": {
    "likes": 1500,
    "retweets": 300,
    "replies": 50
  },
  "ai_title": "username_2024-01-15_rocket_launch"
}
```

---

## Extension Usage

1. **Browse X.com** - Navigate to any tweet
2. **Click archive button** - Look for the download icon in the tweet action bar
3. **Wait for confirmation** - The button shows a checkmark when complete
4. **View history** - Click the extension icon to see recent archives

### Keyboard Shortcut

Press `Alt+S` (or `Option+S` on Mac) to archive the currently visible tweet.

---

## Web UI

XGrabba includes a built-in web interface for browsing your archived tweets.

### Accessing the Web UI

1. **From Browser**: Navigate to your XGrabba server URL (e.g., `http://localhost:9847`)
2. **From Extension**: Click "View All Archives" in the extension popup
3. **First Visit**: Enter your API key when prompted (stored in browser localStorage)

### Features

- Browse all archived tweets with search functionality
- View tweet details, media, and metadata
- Delete archived tweets (removes all files)
- Dark theme matching X.com's design

---

## Samba File Sharing

Enable Samba to browse your archived tweets directly from Mac Finder or Windows Explorer.

### Kubernetes Deployment with Samba

1. **Create secrets** (including Samba password):
   ```bash
   kubectl create secret generic xgrabba-secrets \
     --namespace xgrabba \
     --from-literal=api-key="$(openssl rand -hex 32)" \
     --from-literal=grok-api-key="YOUR_GROK_API_KEY" \
     --from-literal=samba-password="YOUR_SAMBA_PASSWORD"
   ```

2. **Enable Samba in Helm values**:
   ```yaml
   samba:
     enabled: true
     username: xgrabba
     shareName: archives
     service:
       type: LoadBalancer
   ```

3. **Connect from Mac**:
   - Open Finder
   - Press `Cmd+K` or Go > Connect to Server
   - Enter: `smb://YOUR_LOADBALANCER_IP/archives`
   - Login with username `xgrabba` and your Samba password

4. **Connect from Windows**:
   - Open File Explorer
   - In address bar: `\\YOUR_LOADBALANCER_IP\archives`
   - Login with username `xgrabba` and your Samba password

### Samba Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `samba.enabled` | Enable Samba sidecar | `false` |
| `samba.username` | Samba username | `xgrabba` |
| `samba.password` | Samba password | *required* |
| `samba.shareName` | Share name | `archives` |
| `samba.service.type` | Service type | `LoadBalancer` |
| `samba.service.port` | SMB port | `445` |

---

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
├── cmd/server/              # Application entry point
├── internal/
│   ├── api/                 # HTTP handlers and middleware
│   ├── config/              # Configuration management
│   ├── domain/              # Domain entities
│   ├── downloader/          # Media download logic
│   ├── repository/          # Data persistence
│   ├── service/             # Business logic
│   └── worker/              # Background job processing
├── pkg/
│   ├── grok/                # Grok AI client
│   └── twitter/             # Twitter API client
├── extension/               # Chrome/Edge extension
└── deployments/
    ├── helm/xgrabba/        # Helm chart
    └── crossplane/          # Crossplane manifests
```

---

## CI/CD

### GitHub Actions

- **CI** (`ci.yml`): Runs on every push/PR
  - Go build and test
  - Linting with golangci-lint
  - Docker build test
  - Helm chart linting

- **Release** (`release.yml`): Runs on version tags (`v*`)
  - Builds multi-platform binaries
  - Pushes Docker image to GHCR
  - Packages and pushes Helm chart to GHCR OCI registry
  - Creates GitHub release with assets

### Creating a Release

```bash
git tag v0.1.0
git push origin v0.1.0
```

This triggers:
1. Docker image: `ghcr.io/iconidentify/xgrabba:v0.1.0`
2. Helm chart: `oci://ghcr.io/iconidentify/charts/xgrabba:0.1.0`
3. GitHub Release with binaries and extension zip

---

## License

MIT License - See [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests (`make test`)
5. Submit a pull request
