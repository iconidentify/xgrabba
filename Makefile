.PHONY: build run test lint clean docker helm build-export build-viewer build-all

# Variables
BINARY_NAME=xgrabba
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

# Go commands
build:
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/server

# Build export CLI
build-export:
	go build $(LDFLAGS) -o bin/xgrabba-export ./cmd/export

# Build viewer for current platform
build-viewer:
	go build $(LDFLAGS) -o bin/xgrabba-viewer ./cmd/viewer

# Build cross-platform viewers
build-viewer-all:
	@echo "Building Windows viewer..."
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o bin/xgrabba-viewer.exe ./cmd/viewer
	@echo "Building Linux viewer..."
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/xgrabba-viewer-linux ./cmd/viewer
	@echo "Building macOS AMD64 viewer..."
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o bin/xgrabba-viewer-mac-amd64 ./cmd/viewer
	@echo "Building macOS ARM64 viewer..."
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o bin/xgrabba-viewer-mac-arm64 ./cmd/viewer
	@echo "Creating macOS universal binary..."
	@if command -v lipo >/dev/null 2>&1; then \
		lipo -create -output bin/xgrabba-viewer-mac bin/xgrabba-viewer-mac-amd64 bin/xgrabba-viewer-mac-arm64; \
		rm bin/xgrabba-viewer-mac-amd64 bin/xgrabba-viewer-mac-arm64; \
	else \
		mv bin/xgrabba-viewer-mac-amd64 bin/xgrabba-viewer-mac; \
		rm -f bin/xgrabba-viewer-mac-arm64; \
		echo "lipo not available, using AMD64 binary only"; \
	fi
	@echo "Done! Viewers in bin/"

# Build everything including export tools
build-all: build build-export build-viewer-all

run:
	go run ./cmd/server

test:
	go test -v -race -cover ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf bin/ coverage.out coverage.html

# Dependencies
deps:
	go mod download
	go mod tidy

# Docker
docker-build:
	docker build -t $(BINARY_NAME):$(VERSION) .

docker-run:
	docker run -p 8080:8080 \
		-e API_KEY=dev-key \
		-e GROK_API_KEY=your-grok-key \
		-v $(PWD)/data:/data/videos \
		$(BINARY_NAME):$(VERSION)

# Helm
helm-lint:
	helm lint deployments/helm/xgrabba

helm-template:
	helm template xgrabba deployments/helm/xgrabba

helm-package:
	helm package deployments/helm/xgrabba -d dist/

# Development
dev: deps fmt vet test build

# All
all: clean deps fmt vet lint test build
