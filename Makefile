VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: build test release
build:
	go build -ldflags="$(LDFLAGS)" -o bin/ ./cmd/...

test:
	go vet ./... && go test ./...

# Cross-compiled single binaries (no cgo; the bridge loads libopus/NDI at runtime)
release:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  for c in openline-server openline-bridge; do \
	    echo "$$c $$os/$$arch"; \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$$c-$$os-$$arch$$ext ./cmd/$$c || exit 1; \
	  done; \
	done
