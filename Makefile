.PHONY: build run test lint clean docker helm

# Variables
BINARY_NAME=xgrabba
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)"

# Go commands
build:
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/server

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
