APP_NAME := twc-approval-go-bridge
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo 0.3.3)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
VERSION_PKG := github.com/yunyingk/twc-approval-go-bridge/internal/version
LDFLAGS := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).GitCommit=$(GIT_COMMIT) \
	-X $(VERSION_PKG).BuildDate=$(BUILD_DATE)

.PHONY: fmt test vet build run clean docker-build compose-up version check-version bump local-update local-status local-rollback remote-migrate remote-build remote-deploy remote-update remote-status remote-rollback

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

# Update only this checkout's macOS LaunchAgent; retain its config and state.
local-update:
	python3 scripts/local-service.py update

local-status:
	python3 scripts/local-service.py status

local-rollback:
	python3 scripts/local-service.py rollback

# Remote operational settings are CLI arguments, not business environment overrides.
REMOTE_HOST ?= wsl-dev
REMOTE_ROOT ?= /home/dev/apps/twc-approval-go-bridge
BUILD_HOST ?= wsl-dev
BUILD_ROOT ?= /home/dev/apps/twc-builder/twc-approval-go-bridge
ARTIFACT ?=
REMOTE_ARGS = --host "$(REMOTE_HOST)" --remote-root "$(REMOTE_ROOT)"
BUILD_ARGS = --build-host "$(BUILD_HOST)" --build-root "$(BUILD_ROOT)"

remote-build:
	python3 scripts/remote-service.py build $(BUILD_ARGS)

remote-deploy:
	python3 scripts/remote-service.py deploy $(REMOTE_ARGS) $(if $(ARTIFACT),--artifact "$(ARTIFACT)",)
remote-migrate:
	python3 scripts/remote-service.py migrate

remote-update:
	python3 scripts/remote-service.py update $(REMOTE_ARGS) $(BUILD_ARGS)

remote-status:
	python3 scripts/remote-service.py status $(REMOTE_ARGS)

remote-rollback:
	python3 scripts/remote-service.py rollback $(REMOTE_ARGS)

# Isolated kind lab in WSL; never uses an implicit kubectl context.
.PHONY: k8s-lab-up k8s-lab-deploy k8s-lab-status k8s-lab-verify k8s-lab-rollback k8s-lab-stop image-build image-publish
k8s-lab-up:
	python3 -B scripts/k8s-lab.py up --host "$(REMOTE_HOST)"
k8s-lab-deploy:
	python3 -B scripts/k8s-lab.py deploy --host "$(REMOTE_HOST)" $(if $(ARTIFACT),--artifact "$(ARTIFACT)",)
k8s-lab-status:
	python3 -B scripts/k8s-lab.py status --host "$(REMOTE_HOST)"
k8s-lab-verify:
	python3 -B scripts/k8s-lab.py verify --host "$(REMOTE_HOST)"
k8s-lab-rollback:
	python3 -B scripts/k8s-lab.py rollback --host "$(REMOTE_HOST)"
k8s-lab-stop:
	python3 -B scripts/k8s-lab.py stop --host "$(REMOTE_HOST)"
image-build:
	python3 -B scripts/build-image.py
image-publish:
	python3 -B scripts/publish-image.py "$(ARTIFACT)" "$(REGISTRY_IMAGE)"
