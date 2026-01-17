.PHONY: build run test lint clean docker helm build-export build-viewer build-tui build-all

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

# Build TUI for current platform
build-tui:
	go build $(LDFLAGS) -o bin/xgrabba-tui ./cmd/xgrabba-tui

# Build cross-platform TUI binaries
build-tui-all:
	@echo "Building TUI for Linux..."
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/xgrabba-tui-linux ./cmd/xgrabba-tui
	@echo "Building TUI for macOS AMD64..."
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o bin/xgrabba-tui-mac-amd64 ./cmd/xgrabba-tui
	@echo "Building TUI for macOS ARM64..."
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o bin/xgrabba-tui-mac-arm64 ./cmd/xgrabba-tui
	@echo "Creating macOS universal binary..."
	@if command -v lipo >/dev/null 2>&1; then \
		lipo -create -output bin/xgrabba-tui-mac bin/xgrabba-tui-mac-amd64 bin/xgrabba-tui-mac-arm64; \
		rm bin/xgrabba-tui-mac-amd64 bin/xgrabba-tui-mac-arm64; \
	else \
		mv bin/xgrabba-tui-mac-amd64 bin/xgrabba-tui-mac; \
		rm -f bin/xgrabba-tui-mac-arm64; \
		echo "lipo not available, using AMD64 binary only"; \
	fi
	@echo "Done! TUI binaries in bin/"

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

# Build everything including export tools and TUI
build-all: build build-export build-tui build-viewer-all build-tui-all

run:
	go run ./cmd/server

test:
	go test -v -race -cover ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Coverage threshold enforcement (target: 70% test coverage)
COVERAGE_THRESHOLD?=70

# Check coverage meets minimum threshold
test-coverage-check:
	@echo "Running tests with coverage..."
	@go test -coverprofile=coverage.out ./... 2>&1
	@COVERAGE=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	echo "Total coverage: $${COVERAGE}%"; \
	if [ $$(echo "$${COVERAGE} < $(COVERAGE_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "Coverage $${COVERAGE}% is below threshold $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	else \
		echo "Coverage $${COVERAGE}% meets threshold $(COVERAGE_THRESHOLD)%"; \
	fi

# Quick coverage report by package
test-coverage-report:
	@go test -coverprofile=coverage.out ./... 2>/dev/null
	@echo "=== Coverage by Package ==="
	@go tool cover -func=coverage.out | grep -E "(ok|FAIL|total)" | sort -k3 -t: -n

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
