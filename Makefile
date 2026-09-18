APP_NAME := twc-approval-go-bridge
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/yunyingk/twc-approval-go-bridge/internal/version.Version=$(VERSION)

.PHONY: fmt test vet build run clean docker-build compose-up

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
