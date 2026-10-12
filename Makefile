APP_NAME := twc-approval-go-bridge
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo 0.3.3)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
VERSION_PKG := github.com/yunyingk/twc-approval-go-bridge/internal/version
LDFLAGS := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).GitCommit=$(GIT_COMMIT) \
	-X $(VERSION_PKG).BuildDate=$(BUILD_DATE)

.PHONY: fmt test vet build run clean docker-build compose-up version check-version bump

fmt:
	gofmt -w cmd internal

test:
	go test ./...

vet:
	go vet ./...

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME) ./cmd/server

run:
	go run ./cmd/server

clean:
	go clean
	$(RM) bin/$(APP_NAME)

docker-build:
	docker build --file deploy/Dockerfile --tag $(APP_NAME):$(VERSION) .

compose-up:
	docker compose --file deploy/docker-compose.yml up --build

version:
	@echo $(VERSION)

check-version:
	go test -v ./internal/version -run TestVersionConsistency

bump:
	@./scripts/bump-version.sh $(VERSION)
