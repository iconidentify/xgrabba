# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

XGrabba is an X.com (Twitter) tweet archiving tool written in Go. It archives tweets including all media (images, videos, GIFs) with AI-powered naming using Grok AI and optional audio transcription via Whisper.

## Common Commands

```bash
# Development
make run              # Run server locally (loads .env automatically)
make build            # Build binary to bin/xgrabba
make test             # Run all tests with race detection and coverage
make lint             # Run golangci-lint
make fmt              # Format code

# Run a single test
go test -v -run TestName ./path/to/package

# Docker
make docker-build     # Build Docker image

# Helm
make helm-lint        # Lint Helm chart
make helm-template    # Render Helm templates
```

## Architecture

The application follows clean architecture with clear separation:

```
cmd/server/main.go     # Entry point - wires up all dependencies
internal/
  api/                 # HTTP layer (chi router, handlers, middleware)
  config/              # Configuration via envconfig
  domain/              # Core entities (Tweet, Job, Video, errors)
  service/             # Business logic (TweetService, VideoService)
  repository/          # Data persistence (filesystem-based)
  downloader/          # HTTP media download with retry logic
  worker/              # Background job pool for async processing
  bookmarks/           # X Bookmarks polling monitor and OAuth store
pkg/
  grok/                # Grok AI client for filename generation
  twitter/             # Twitter API clients (syndication, bookmarks, OAuth)
  whisper/             # OpenAI Whisper client for transcription
  ffmpeg/              # Video processing utilities
extension/             # Chrome/Edge browser extension
deployments/
  helm/xgrabba/        # Helm chart
  crossplane/          # Crossplane manifests for GitOps
```

### Key Data Flow

1. **Tweet Archival**: Browser extension or bookmarks monitor sends tweet URL to `/api/v1/tweets`
2. `TweetService` fetches tweet data from Twitter's syndication API (no auth required)
3. Grok AI generates a descriptive filename
4. Media is downloaded, optionally transcribed via Whisper
5. Tweet saved to filesystem as JSON + Markdown with organized media

### Storage Layout

Tweets are stored at `$STORAGE_PATH/YYYY/MM/username_date_tweetID/` with:
- `tweet.json` - Full metadata
- `README.md` - Human-readable summary
- `media/` - Downloaded images/videos

### Configuration

Environment variables loaded via `envconfig`. Key ones:
- `API_KEY` - Required for API authentication
- `GROK_API_KEY` - Required for AI naming
- `STORAGE_PATH` - Where tweets are archived (default: `/data/videos`)
- `OPENAI_API_KEY` - Optional for Whisper transcription
- `BOOKMARKS_ENABLED` - Enable auto-archive of X bookmarks

## Code Style Notes

- Uses go-chi/chi for routing
- JSON logging via slog
- Tests use standard Go testing (no external framework)
- The linter config in `.golangci.yml` excludes certain error checks for HTTP response writes and defer Close()

## Two Archive Systems

The codebase has two systems that coexist:
1. **Tweet system** (current, recommended) - `TweetService`, `TweetHandler`, `/api/v1/tweets`
2. **Video system** (legacy) - `VideoService`, `VideoHandler`, `/api/v1/videos` - kept for backwards compatibility
